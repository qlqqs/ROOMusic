#!/usr/bin/env python3
"""使用合成配置验证宿主机首次安装流程，仅替换提权相关命令。"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ProductionInitTest(unittest.TestCase):
    def setUp(self):
        (ROOT / 'temp').mkdir(exist_ok=True)
        self.directory = tempfile.TemporaryDirectory(prefix='production-init-', dir=ROOT / 'temp')
        self.addCleanup(self.directory.cleanup)
        self.work = Path(self.directory.name)
        shutil.copy(ROOT / 'compose.prod.yaml', self.work / 'compose.yaml')
        self.bin = self.work / 'bin'
        self.bin.mkdir()
        # 本地测试不提权；真实 chmod、密码写入和 Compose 配置解析保持不变。
        for name, body in {'id': 'echo 0', 'chown': 'printf "%s\\n" "$@" >> "$TEST_CHOWN_LOG"'}.items():
            executable = self.bin / name
            executable.write_text('#!/bin/sh\n' + body + '\n')
            executable.chmod(0o755)
        self.env = {'PATH': str(self.bin) + ':' + os.environ['PATH'],
                    'HOME': os.environ['HOME'], 'TEST_CHOWN_LOG': str(self.work / 'chown.log')}
        self.configure()

    def configure(self, extra=''):
        (self.work / '.env').write_text('ROOMUSIC_MUSIC_DIR=/synthetic/music\n' + extra)

    def run_init(self, success=True):
        result = subprocess.run(['sh', str(ROOT / 'scripts/production-init.sh')],
                                cwd=self.work, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stderr)
        return result

    def test_random_password_reuse_and_permissions(self):
        self.run_init()
        password = self.work / 'secrets/postgres_password'
        original = password.read_bytes()
        self.assertRegex(original.decode(), r'^[0-9a-f]{64}$')
        self.assertEqual(password.stat().st_mode & 0o777, 0o600)
        self.assertEqual((self.work / 'postgres').stat().st_mode & 0o777, 0o755)
        for name in ('secrets', 'data', 'backups'):
            self.assertEqual((self.work / name).stat().st_mode & 0o777, 0o700)
        self.assertIn('1000:1000', (self.work / 'chown.log').read_text())
        self.run_init()
        self.assertEqual(password.read_bytes(), original)

    def test_literal_password_and_custom_backup(self):
        value = 'synthetic $value $$literal = $(touch injected)'
        self.configure("ROOMUSIC_DB_PASSWORD='" + value + "'\nROOMUSIC_BACKUP_DIR='./backup space'\n")
        self.run_init()
        self.assertEqual((self.work / 'secrets/postgres_password').read_text(), value)
        self.assertTrue((self.work / 'backup space').is_dir())
        self.assertFalse((self.work / 'injected').exists())

    def test_password_mismatch_keeps_original(self):
        self.run_init()
        password = self.work / 'secrets/postgres_password'
        original = password.read_bytes()
        self.configure('ROOMUSIC_DB_PASSWORD=other-synthetic-password\n')
        result = self.run_init(False)
        self.assertNotIn('other-synthetic-password', result.stdout + result.stderr)
        self.assertEqual(password.read_bytes(), original)

    def test_existing_database_without_password(self):
        (self.work / 'postgres').mkdir()
        (self.work / 'postgres/PG_VERSION').write_text('18')
        self.run_init(False)
        self.assertFalse((self.work / 'secrets/postgres_password').exists())

    def test_empty_password_file(self):
        (self.work / 'secrets').mkdir()
        (self.work / 'secrets/postgres_password').touch()
        self.run_init(False)

    def test_multiline_password_rejected(self):
        self.configure("ROOMUSIC_DB_PASSWORD='line-one\nkey=value'\n")
        self.run_init(False)
        self.assertFalse((self.work / 'secrets').exists())

    def test_non_root_rejected(self):
        (self.bin / 'id').write_text('#!/bin/sh\necho 1000\n')
        self.run_init(False)
        self.assertFalse((self.work / 'secrets').exists())


if __name__ == '__main__':
    unittest.main()
