#!/usr/bin/env python3
"""用真实 Docker 串行验证生产部署。运行 sg docker -c 'python3 scripts/tests/test_production_deployment.py -v'。"""

import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener, urlopen
import uuid
import wave

ROOT = Path(__file__).resolve().parents[2]
TEMP = ROOT / "temp"
MAIN_COMPOSE = ROOT / "compose.prod.yaml"
NAS_COMPOSE = ROOT / "deploy/nas/compose.yaml"
ALPINE = "alpine:3.23@sha256:f3d55900449227ae9e237ede521e7b665d53a253b42065ba90b9db9275e198ad"
LEGACY_IMAGE = "ghcr.io/qlqqs/roomusic:0.8.0"
COMPOSE_VERSION = "v2.20.1"
COMPOSE_ASSET = "docker-compose-linux-x86_64"
COMPOSE_SHA256 = "804aa825c58b375901ba9dc9d0aa1648ce305d903a58f2a5c42bb5c6873b02bf"
DOCKER = ["docker", "--context", "default"]
ENV = {
    key: value
    for key, value in os.environ.items()
    if not key.startswith(("ROOMUSIC_", "COMPOSE_")) and key not in {"PUID", "PGID"}
}
ADMIN = {"username": "deployment-admin", "password": "synthetic-admin-password"}
STATE_DIRS = ("secrets", "data", "backups")
UP = ("up", "-d", "--wait", "--wait-timeout", "120")


def run(argv, *, cwd=ROOT, values=None, check=True, timeout=180, stdin=None):
    result = subprocess.run(
        argv, cwd=cwd, env={**ENV, **(values or {})}, input=stdin,
        text=True, capture_output=True, timeout=timeout,
    )
    if check and result.returncode:
        raise RuntimeError(
            f"{' '.join(argv[:4])} 退出码 {result.returncode}\n{result.stderr}\n{result.stdout}"
        )
    return result


def temporary_directory():
    TEMP.mkdir(exist_ok=True)
    return tempfile.TemporaryDirectory(prefix="roomusic-compose-test-", dir=TEMP)


def nas_compose(folder, music):
    return NAS_COMPOSE.read_text().replace("/path/to/roomusic", str(folder)).replace(
        "/path/to/music", str(music)
    )


def write_music_env(folder, music):
    (folder / ".env").write_text(f"ROOMUSIC_MUSIC_DIR={json.dumps(str(music), ensure_ascii=False)}\n")


def unused_ports():
    with socket.socket() as http_listener, socket.socket() as db_listener:
        http_listener.bind(("0.0.0.0", 0))
        db_listener.bind(("127.0.0.1", 0))
        return http_listener.getsockname()[1], db_listener.getsockname()[1]


class Deployment:
    def __init__(self, folder, music, image, identity):
        self.folder = folder
        self.music = music
        self.image = image
        self.identity = identity
        self.name = folder.parent.name
        self.extra = folder.parent / "resources.json"
        self.http_port, self.db_port = unused_ports()
        self.url = f"http://127.0.0.1:{self.http_port}"

    def configure(self, variant):
        repository, tag = self.image.rsplit(":", 1)
        uid, gid = self.identity
        self.env = {
            "ROOMUSIC_IMAGE": repository,
            "ROOMUSIC_VERSION": tag,
            "ROOMUSIC_APP_CONTAINER_NAME": f"{self.name}-app",
            "ROOMUSIC_POSTGRES_CONTAINER_NAME": f"{self.name}-postgres",
            "ROOMUSIC_HTTP_PORT": str(self.http_port),
            "ROOMUSIC_DB_PORT": str(self.db_port),
        }
        if self.identity != (1000, 1000):
            self.env.update(PUID=str(uid), PGID=str(gid))
        override = {
            "app": {"cpus": 2, "mem_limit": "1g"},
            "postgres": {"cpus": 1, "mem_limit": "512m"},
        }
        if variant == "nas":
            # 单文件中的端口是字面值。隔离测试端口，同时保留原来的监听方式。
            compose = nas_compose(self.folder, self.music)
            compose = compose.replace("0.0.0.0:8090", f"0.0.0.0:{self.http_port}")
            compose = compose.replace("127.0.0.1:55439", f"127.0.0.1:{self.db_port}")
            override["app"].update(image=self.image, container_name=self.env["ROOMUSIC_APP_CONTAINER_NAME"])
            override["postgres"]["container_name"] = self.env["ROOMUSIC_POSTGRES_CONTAINER_NAME"]
        else:
            write_music_env(self.folder, self.music)
            if variant == "legacy":
                self.env["ROOMUSIC_VERSION"] = "0.8.0"
                compose = run(["git", "show", "v0.8.0:compose.prod.yaml"]).stdout
            else:
                compose = MAIN_COMPOSE.read_text()
        (self.folder / "compose.yaml").write_text(compose)
        self.extra.write_text(json.dumps({"services": override}))

    def compose(self, *args, check=True):
        return run(
            [*DOCKER, "compose", "-p", self.name, "-f", "compose.yaml", "-f", str(self.extra), *args],
            cwd=self.folder, values=self.env, check=check,
        )

    def helper(self, script, *args):
        return run([
            *DOCKER, "run", "--rm", "--name", f"{self.name}-helper",
            "--network", "none", "--user", "0:0", "--cpus", "0.5", "--memory", "128m",
            "--mount", f"type=bind,src={self.folder},dst=/work", "--workdir", "/work",
            ALPINE, "sh", "-ec", script, "deployment-test", *args,
        ]).stdout.strip()

    def down(self):
        self.compose("down", "--remove-orphans", "--volumes")

    def cleanup(self):
        if (self.folder / "compose.yaml").is_file():
            self.down()
        if any((self.folder / name).exists() for name in (*STATE_DIRS, "postgres")):
            self.helper('rm -rf -- "$@"', *STATE_DIRS, "postgres")

    def up(self):
        result = self.compose(*UP, check=False)
        if result.returncode:
            logs = self.compose("logs", "--no-color", "--tail", "40", check=False)
            raise AssertionError(f"Compose 启动失败\n{result.stderr}\n{logs.stdout}")
        self.request("/readyz")

    def request(self, path, status=200, *, body=None, session=None, idempotency_key=None):
        headers = {"Origin": self.url}
        if body is not None:
            headers["Content-Type"] = "application/json"
        if idempotency_key is not None:
            headers["Idempotency-Key"] = idempotency_key
        data = None if body is None else json.dumps(body).encode()
        opener = session or build_opener(ProxyHandler({}))
        try:
            response = opener.open(Request(self.url + path, data, headers), timeout=10)
        except HTTPError as error:
            response = error
        with response:
            if response.status != status:
                raise AssertionError(f"{path} 返回 HTTP {response.status}，预期 {status}")
            if response.headers.get_content_type() == "application/json":
                return json.load(response)

    def login(self):
        session = build_opener(ProxyHandler({}), HTTPCookieProcessor(http.cookiejar.CookieJar()))
        user = self.request("/api/v1/auth/login", body=ADMIN, session=session)
        return session, user

    def password_digest(self):
        return self.helper("sha256sum secrets/postgres_password | cut -d ' ' -f 1")


class ProductionDeploymentTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if run([*DOCKER, "image", "inspect", ALPINE], check=False).returncode:
            run([*DOCKER, "pull", ALPINE])
            cls.addClassCleanup(run, [*DOCKER, "image", "rm", ALPINE])
        cls.image = f"roomusic-compose-test:{uuid.uuid4().hex}"
        run(
            [*DOCKER, "build", "-f", "deploy/production/Dockerfile", "-t", cls.image, "."],
            timeout=1200,
        )
        cls.addClassCleanup(run, [*DOCKER, "image", "rm", cls.image])

    def deployment(self, variant="main", identity=(1000, 1000)):
        temporary = temporary_directory()
        self.addCleanup(temporary.cleanup)
        work = Path(temporary.name)
        folder = work / "deployment"
        music = work / "music"
        folder.mkdir(mode=0o755)
        music.mkdir(mode=0o755)
        deployment = Deployment(folder, music, self.image, identity)
        self.addCleanup(deployment.cleanup)
        deployment.configure(variant)
        return deployment

    def test_fresh_install_setup_and_scan(self):
        deployment = self.deployment()
        self.assertEqual({p.name for p in deployment.folder.iterdir()}, {"compose.yaml", ".env"})
        with wave.open(str(deployment.music / "synthetic.wav"), "wb") as audio:
            audio.setparams((1, 2, 44100, 0, "NONE", "not compressed"))
            audio.writeframes(b"\x00\x00" * 44100)
        deployment.up()
        deployment.helper("grep -Exq '[0-9a-f]{64}' secrets/postgres_password")
        admin = deployment.request("/api/v1/setup/admin", 201, body=ADMIN)
        self.assertEqual((admin["username"], admin["role"]), (ADMIN["username"], "admin"))
        session, user = deployment.login()
        self.assertEqual(user, admin)
        deployment.request(
            "/api/v1/library-roots", 201, body={"path": "/music"}, session=session,
            idempotency_key="deployment-music-root",
        )
        scan = deployment.request(
            "/api/v1/scans", 202, body={}, session=session,
            idempotency_key="deployment-scan",
        )
        deadline = time.monotonic() + 60
        while scan["finished_at"] is None and time.monotonic() < deadline:
            time.sleep(0.5)
            scan = deployment.request(f"/api/v1/scans/{scan['id']}", session=session)
        self.assertEqual(scan["status"], "succeeded", scan)
        self.assertEqual(scan["progress"]["files_processed"], 1)
        self.assertEqual(scan["skipped_files"], 0)

    def test_down_up_reuses_password(self):
        deployment = self.deployment()
        deployment.up()
        password_digest = deployment.password_digest()
        deployment.down()
        deployment.up()
        self.assertTrue(deployment.password_digest() == password_digest, "密码文件内容已变化")

    def test_existing_database_without_password_is_rejected(self):
        deployment = self.deployment()
        deployment.up()
        deployment.down()
        deployment.helper("test -s postgres/18/docker/PG_VERSION && rm secrets/postgres_password")
        result = deployment.compose(*UP, "postgres", check=False)
        self.assertNotEqual(result.returncode, 0, "缺少密码文件时 PostgreSQL 启动成功")
        logs = deployment.compose("logs", "--no-color", "postgres").stdout
        self.assertIn("缺少密码文件", logs)
        container = deployment.compose("ps", "-a", "-q", "postgres").stdout.strip()
        exit_code = run([*DOCKER, "inspect", "--format", "{{.State.ExitCode}}", container]).stdout
        self.assertEqual(exit_code.strip(), "1")
        deployment.helper("test -s postgres/18/docker/PG_VERSION && test ! -e secrets/postgres_password")

    def assert_writable(self, deployment):
        uid, gid = deployment.identity
        expected = f"{uid}:{gid}"
        self.assertEqual(
            deployment.helper("stat -c '%u:%g:%a' secrets data backups secrets/postgres_password").splitlines(),
            [f"{expected}:700"] * 3 + [f"{expected}:600"],
        )
        deployment.compose(
            "exec", "-T", "app", "sh", "-ec",
            'test "$(id -u):$(id -g)" = "$1"; printf write-probe > /data/write-probe',
            "app-test", expected,
        )
        deployment.compose("exec", "-T", "app", "/app/roomusic", "backup")
        deployment.helper(
            'test -s data/write-probe; set -- backups/*-manual.dump; test -s "$1"'
        )

    def check_nas_owner(self, uid, gid):
        deployment = self.deployment(identity=(uid, gid))
        deployment.helper(
            'mkdir -m 0700 secrets data backups; touch data/nas-owner-probe; '
            'chown "$1" secrets data backups',
            f"{uid}:{gid}",
        )
        deployment.up()
        self.assert_writable(deployment)
        self.assertEqual(deployment.helper("stat -c '%u:%g' data/nas-owner-probe"), "0:0")

    def test_nas_owner_synology_1026_100(self):
        self.check_nas_owner(1026, 100)

    def test_nas_owner_1000_10(self):
        self.check_nas_owner(1000, 10)

    def test_nas_owner_unraid_99_100(self):
        self.check_nas_owner(99, 100)

    def test_nas_owner_truenas_568_568(self):
        self.check_nas_owner(568, 568)

    def test_docker_created_directories_change_owner(self):
        deployment = self.deployment(identity=(1026, 100))
        self.assertFalse(any((deployment.folder / name).exists() for name in STATE_DIRS))
        deployment.up()
        self.assert_writable(deployment)

    def test_single_file_without_env(self):
        deployment = self.deployment("nas")
        self.assertEqual({p.name for p in deployment.folder.iterdir()}, {"compose.yaml"})
        deployment.up()
        admin = deployment.request("/api/v1/setup/admin", 201, body=ADMIN)
        self.assertEqual((admin["username"], admin["role"]), (ADMIN["username"], "admin"))

    def test_upgrade_from_0_8_0(self):
        if run([*DOCKER, "image", "inspect", LEGACY_IMAGE], check=False).returncode:
            try:
                pull = run([*DOCKER, "pull", LEGACY_IMAGE], check=False)
            except subprocess.TimeoutExpired:
                self.skipTest(f"拉取 {LEGACY_IMAGE} 超时")
            if pull.returncode:
                self.skipTest(f"无法拉取 {LEGACY_IMAGE}：{pull.stderr.strip()}")
            self.addCleanup(run, [*DOCKER, "image", "rm", LEGACY_IMAGE])
        deployment = self.deployment("legacy")
        # 复现旧安装脚本的目录、属主和权限，交给官方 PostgreSQL 入口初始化。
        deployment.helper(
            "umask 077; mkdir secrets data backups; mkdir -m 0755 postgres; "
            "od -An -v -N32 -tx1 /dev/urandom | tr -d ' \\n' > secrets/postgres_password; "
            "chown 1000:1000 secrets secrets/postgres_password data backups"
        )
        deployment.up()
        admin = deployment.request("/api/v1/setup/admin", 201, body=ADMIN)
        password_digest = deployment.password_digest()
        deployment.configure("main")
        deployment.up()
        self.assertTrue(deployment.password_digest() == password_digest, "升级修改了密码文件")
        session, user = deployment.login()
        self.assertEqual(user, admin)
        self.assertEqual(deployment.request("/api/v1/auth/me", session=session), admin)


class ComposeCompatibilityTests(unittest.TestCase):
    def test_compose_v2_20_1(self):
        with temporary_directory() as temporary:
            work = Path(temporary)
            binary = work / COMPOSE_ASSET
            release_url = f"https://github.com/docker/compose/releases/download/{COMPOSE_VERSION}/{COMPOSE_ASSET}"
            with urlopen(release_url, timeout=60) as response, binary.open("wb") as target:
                shutil.copyfileobj(response, target)
            with binary.open("rb") as downloaded:
                checksum = hashlib.file_digest(downloaded, "sha256").hexdigest()
            self.assertEqual(checksum, COMPOSE_SHA256)
            binary.chmod(0o700)
            version = run([str(binary), "version", "--short"], cwd=work).stdout.strip()
            self.assertEqual(version.removeprefix("v"), COMPOSE_VERSION.removeprefix("v"))

            music = work / "music"
            music.mkdir()
            for variant in ("main", "nas"):
                with self.subTest(variant=variant):
                    folder = work / variant
                    folder.mkdir()
                    if variant == "main":
                        write_music_env(folder, music)
                        compose = MAIN_COMPOSE.read_text()
                    else:
                        compose = nas_compose(folder, music)
                    (folder / "compose.yaml").write_text(compose)
                    run([str(binary), "-f", "compose.yaml", "config", "--quiet"], cwd=folder)


if __name__ == "__main__":
    unittest.main()
