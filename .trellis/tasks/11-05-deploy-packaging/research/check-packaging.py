#!/usr/bin/env python3
"""部署打包的安全局部检查：不启动容器、不连接数据库、不读取本机配置。"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[4]


def check_scripts():
    scripts = sorted((ROOT / "scripts").glob("*.sh")) + sorted(
        (ROOT / "deploy/production").glob("*.sh")
    )
    for script in scripts:
        subprocess.run(["bash", "-n", str(script)], check=True)
    print(f"PASS: bash -n，逐个检查 {len(scripts)} 个脚本")
    changed = [ROOT / "scripts" / name for name in (
        "production.sh", "dev.sh", "dev-reset.sh", "reset-password.sh", "ffmpeg-pinned.sh"
    )] + sorted((ROOT / "deploy/production").glob("*.sh"))
    if shutil.which("shellcheck"):
        subprocess.run(["shellcheck", *map(str, changed)], check=True)
        print("PASS: shellcheck")
    else:
        print("SKIP: shellcheck 未安装")


def check_production_env():
    script = str(ROOT / "scripts/production.sh")
    subprocess.run([script, "--help"], check=True, stdout=subprocess.DEVNULL)
    with tempfile.TemporaryDirectory(prefix="roomusic-env-check.") as directory:
        base = Path(directory)
        config = base / "prod.env"
        example = (ROOT / ".env.prod.example").read_text()
        config.write_text(example)
        config.chmod(0o600)
        env = dict(os.environ, ROOMUSIC_PROD_ENV_FILE=str(config),
                   ROOMUSIC_HTTP_PORT="99999", COMPOSE_PROJECT_NAME="roomusic")

        def run(*args):
            return subprocess.run([script, *args], env=env, text=True, capture_output=True)

        result = run("--dry-run", "stop")
        assert result.returncode == 0, result.stderr
        assert " -p roomusic-prod " in result.stdout
        assert result.stdout.rstrip().endswith("stop")
        assert " down" not in result.stdout and " -v " not in result.stdout
        assert run("reset-password", "").returncode == 2
        env["ROOMUSIC_PROD_ENV_FILE"] = str(base / "missing")
        assert run("--dry-run", "stop").returncode == 1
        env["ROOMUSIC_PROD_ENV_FILE"] = str(config)
        config.write_text(example.replace("ROOMUSIC_HTTP_PORT=8090", "ROOMUSIC_HTTP_PORT=99999"))
        assert run("--dry-run", "stop").returncode == 1
        marker = base / "must-not-exist"
        config.write_text(example.replace("ROOMUSIC_HTTP_PORT=8090", f"ROOMUSIC_HTTP_PORT=$(touch {marker})"))
        assert run("--dry-run", "stop").returncode == 1
        assert not marker.exists(), "配置文件不能作为 shell 执行"
    print("PASS: production --help、dry-run stop、继承环境隔离、参数/配置错误、禁止 source")


def check_backup():
    busybox = shutil.which("busybox")
    if not busybox:
        print("SKIP: BusyBox 未安装，备份替身检查未运行")
        return
    with tempfile.TemporaryDirectory(prefix="roomusic-backup-check.") as directory:
        base = Path(directory)
        backups = base / "backups"
        backups.mkdir()
        commands = base / "bin"
        commands.mkdir()
        for tool in ["mktemp", "stat", "date", "find", "ln", "rm", "touch", "sleep", "sed"]:
            (commands / tool).symlink_to(busybox)
        (commands / "pg_dump").write_text("""#!/bin/sh
[ "${FAIL_DUMP:-0}" = 0 ] || { echo 'secret-must-not-leak' >&2; exit 7; }
printf 'synthetic dump' > "$3"
""")
        (commands / "pg_restore").write_text("""#!/bin/sh
[ "${FAIL_LIST:-0}" = 0 ] || exit 8
exit 0
""")
        for executable in commands.glob("pg_*"):
            executable.chmod(0o755)
        script = base / "backup.sh"
        # 只在临时副本中重定向备份目录；数据库命令用合成替身，不碰真实目录或服务器。
        script.write_text((ROOT / "deploy/production/backup.sh").read_text().replace(
            "backup_dir=/backups", f"backup_dir='{backups}'"
        ))
        env = dict(os.environ, PATH=str(commands) + ":" + os.environ["PATH"])

        def run(mode, **updates):
            return subprocess.run([busybox, "sh", str(script), mode], env=env | updates,
                                  text=True, capture_output=True)

        assert run("check").returncode == 1
        result = run("now")
        assert result.returncode == 0, result.stderr
        assert json.loads(result.stdout)["event"] == "backup.completed"
        assert len(list(backups.glob("*.dump"))) == 1
        assert run("check").returncode == 0
        assert next(backups.glob("*.dump")).stat().st_mode & 0o777 == 0o600
        marker = (backups / ".last-success").stat().st_mtime_ns
        stale = backups / "roomusic-20000101-000000.dump"
        stale.write_text("old")
        os.utime(stale, (time.time() - 15 * 86400,) * 2)
        for flags, code in [({"FAIL_DUMP": "1"}, 7), ({"FAIL_LIST": "1"}, 8)]:
            failed = run("now", **flags)
            assert failed.returncode == 1
            assert "secret-must-not-leak" not in failed.stdout + failed.stderr
            assert json.loads(failed.stderr)["exit_code"] == code
            assert stale.exists()
            assert (backups / ".last-success").stat().st_mtime_ns == marker
            assert not list(backups.glob(".*partial*"))
        assert run("now").returncode == 0
        assert not stale.exists() and len(list(backups.glob("*.dump"))) == 2
        os.utime(backups / ".last-success", (time.time() - 27 * 3600,) * 2)
        assert run("check").returncode == 1
        for invalid in ["24", "03", "-1", "abc"]:
            assert run("now", ROOMUSIC_BACKUP_HOUR=invalid).returncode == 2
        for invalid in ["0", "-1", "abc"]:
            assert run("now", ROOMUSIC_BACKUP_KEEP_DAYS=invalid).returncode == 2
        process = subprocess.Popen([busybox, "sh", str(script), "loop"], env=env,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline and len(list(backups.glob("*.dump"))) < 3:
                time.sleep(0.05)
        finally:
            process.terminate()
            output, errors = process.communicate(timeout=3)
        assert process.returncode == 0, (output, errors)
        assert len(list(backups.glob("*.dump"))) == 3
    print("PASS: BusyBox 备份成功/权限、失败脱敏/清理、不覆盖、保留期、健康检查、参数、loop 补备份/退出")


def check_entrypoint_and_restore():
    with tempfile.TemporaryDirectory(prefix="roomusic-entry-check.") as directory:
        base = Path(directory)
        commands = base / "bin"
        commands.mkdir()
        (commands / "pg_isready").write_text(f"""#!/bin/sh
count=0
[ ! -f '{base}/count' ] || count=$(cat '{base}/count')
echo $((count + 1)) > '{base}/count'
[ "$count" -ge 2 ]
""")
        (commands / "sleep").write_text("#!/bin/sh\nexit 0\n")
        (commands / "app").write_text("#!/bin/sh\nprintf 'started:%s\\n' \"$*\"\n")
        for path in commands.iterdir():
            path.chmod(0o755)
        script = base / "entrypoint.sh"
        script.write_text((ROOT / "deploy/production/entrypoint.sh").read_text().replace(
            "/app/roomusic", str(commands / "app")
        ))
        env = dict(os.environ, PATH=str(commands) + ":" + os.environ["PATH"],
                   ROOMUSIC_DATABASE_URL="postgres://redacted:secret@invalid/test")
        result = subprocess.run(["sh", str(script), "test-argument"], env=env,
                                text=True, capture_output=True)
        assert result.returncode == 0 and "started:test-argument" in result.stdout
        assert "platform.database.waiting" in result.stdout
        assert "secret" not in result.stdout + result.stderr
        assert (base / "count").read_text().strip() == "3"
        restore = str(ROOT / "deploy/production/restore.sh")
        subprocess.run([restore, "--help"], check=True, stdout=subprocess.DEVNULL)
        backup = base / "fake.dump"
        backup.write_text("synthetic")
        result = subprocess.run([restore, "production", str(backup)], capture_output=True,
                                env=dict(os.environ, COMPOSE_PROJECT_NAME="roomusic"))
        assert result.returncode == 2
    print("PASS: 入口等待/参数透传/脱敏、恢复帮助与拒绝开发项目（未调用 Docker）")


if __name__ == "__main__":
    check_scripts()
    check_production_env()
    check_backup()
    check_entrypoint_and_restore()
