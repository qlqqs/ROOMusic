#!/usr/bin/env python3
"""在临时 Git 仓库验证发布前门禁，绝不修改真实仓库的引用或远端。"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "release-check.sh"


class ReleaseCheckTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="roomusic-release-check-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.env = dict(os.environ)
        # 不继承真实仓库引用、hooks 或用户 Git 配置；origin 是临时本地裸仓库。
        for key in list(self.env):
            if key.startswith("GIT_"):
                del self.env[key]
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null",
                        GIT_TERMINAL_PROMPT="0")
        self.git("init", "-b", "main")
        self.git("config", "user.name", "发布门禁测试")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("config", "core.hooksPath", "/dev/null")
        self.write("scripts/release-check.sh", SCRIPT.read_text())
        self.write("backend/cmd/roomusic/web/index.html", "合成前端\n")
        self.commit()
        self.origin = self.root / "origin.git"
        self.git("clone", "--bare", str(self.repo), str(self.origin))
        self.git("remote", "add", "origin", str(self.origin))

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, env=self.env,
                              text=True, capture_output=True, check=True).stdout.strip()

    def write(self, path, content):
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-m", "临时测试夹具")

    def check(self, version="v1.0.0", success=False, contains=None, hidden=None):
        before = self.git("show-ref", "--heads", "--tags")
        status = self.git("status", "--porcelain", "--untracked-files=all")
        result = subprocess.run(["bash", "scripts/release-check.sh", version],
                                cwd=self.repo, env=self.env, text=True,
                                capture_output=True, timeout=20)
        output = result.stdout + result.stderr
        self.assertEqual(result.returncode == 0, success, output)
        if contains:
            self.assertIn(contains, output)
        if hidden:
            self.assertNotIn(hidden, output)
        self.assertEqual(self.git("show-ref", "--heads", "--tags"), before)
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all"), status)
        return output

    def test_valid_initial_release_and_example_configuration(self):
        self.write("nested/.env.dev.example", "EXAMPLE=placeholder\n")
        self.write("nested/.env.prod.example", "EXAMPLE=placeholder\n")
        self.commit()
        self.check(success=True, contains="发布检查通过")

    def test_invalid_versions(self):
        for version in ["1.0.0", "v01.0.0", "v1.00.0", "v1.0.00", "v1.0.0-check", "v1.0", "v1.0.0\n"]:
            with self.subTest(version=version):
                self.check(version, contains="版本必须是")

    def test_version_must_exceed_existing_tag(self):
        # 仅给临时夹具加引用，不在真实仓库打标签，也不执行任何 push。
        self.git("update-ref", "refs/tags/v1.2.0", "HEAD")
        self.check("v1.2.0", contains="版本标签已存在")
        self.check("v1.1.9", contains="新版本必须大于")
        self.check("v1.2.1", success=True)
        self.check("v1.10.0", success=True)

    def test_dirty_tracked_and_untracked_worktree(self):
        self.write("untracked.txt", "合成未跟踪文件\n")
        self.check(contains="工作区必须干净")
        (self.repo / "untracked.txt").unlink()
        self.write("backend/cmd/roomusic/web/index.html", "已修改\n")
        self.check(contains="工作区必须干净")

    def test_main_branch_required(self):
        self.git("checkout", "-b", "test-branch")
        self.check(contains="当前分支必须是 main")

    def test_remote_main_must_be_ancestor(self):
        self.write("later.txt", "只在临时origin的提交\n")
        self.commit()
        later = self.git("rev-parse", "HEAD")
        self.git("fetch", str(self.repo), "main", "--no-tags")
        subprocess.run(["git", "--git-dir", str(self.origin), "fetch", str(self.repo),
                        "main:main"], env=self.env, check=True, capture_output=True)
        self.git("reset", "--hard", later + "^")
        self.check(contains="远端 main 不是本地 HEAD 的祖先")

    def test_sensitive_filenames_are_rejected_without_contents(self):
        secret = "仅供门禁测试的合成秘密，不得打印"
        for name in [".env", ".env.prod", "nested/test.pem", "nested/test.key", "nested/test.p12", "nested/id_rsa_test", "backup.dump"]:
            with self.subTest(name=name):
                self.write(name, secret)
                self.commit()
                self.check(contains="禁止发布敏感文件", hidden=secret)
                self.git("reset", "--hard", "HEAD^")

    def test_added_tokens_are_rejected_and_redacted(self):
        tokens = [prefix + "SYNTHETIC_TEST_ONLY" for prefix in ["ghp_", "github_pat_", "gho_", "glpat-"]]
        tokens += ["AKIA" + "A" * 16, "-----BEGIN " + "RSA PRIVATE KEY-----",
                   "xoxb-" + "SYNTHETIC-TEST", "sk-" + "a" * 24]
        for token in tokens:
            with self.subTest(prefix=token[:4]):
                self.write("notes.txt", "合成第一行\n" + token + "\n")
                self.commit()
                self.check(contains="'notes.txt':2", hidden=token)
                self.git("reset", "--hard", "HEAD^")

    def test_initial_commit_is_scanned(self):
        # 首发也必须覆盖根提交，不能只检查根提交以后的差异。
        self.write(".env", "INITIAL_SECRET_MUST_NOT_APPEAR")
        self.git("add", ".env")
        self.git("commit", "--amend", "--no-edit")
        # 同步临时 origin 的 main 到这个根提交（从未接触真实远端）。
        self.git("remote", "remove", "origin")
        shutil.rmtree(self.origin)
        self.git("clone", "--bare", str(self.repo), str(self.origin))
        self.git("remote", "add", "origin", str(self.origin))
        self.check(contains="禁止发布敏感文件", hidden="INITIAL_SECRET_MUST_NOT_APPEAR")


if __name__ == "__main__":
    unittest.main(verbosity=2)
