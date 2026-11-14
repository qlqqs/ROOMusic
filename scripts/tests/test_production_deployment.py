#!/usr/bin/env python3
"""使用独立目录和 Compose 项目验证生产部署；镜像由调用者预先构建。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    args = parser.parse_args()
    (ROOT / 'temp').mkdir(exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='deployment-', dir=ROOT / 'temp'))
    prefix = 'roomusic-deploy-test-' + uuid.uuid4().hex[:10]
    # 不继承任何部署变量，不读取现有实例配置。
    env = {k: v for k, v in os.environ.items() if not k.startswith(('ROOMUSIC_', 'COMPOSE_'))}
    projects = []

    def command(cmd, *, input=None, check=True):
        return subprocess.run(cmd, input=input, text=True, env=env,
                              capture_output=True, check=check)

    def helper(script):
        return command(['docker', 'run', '--rm', '--network', 'none', '--user', '0:0',
                        '--entrypoint', '/bin/sh', '--mount', f'type=bind,src={work},dst=/work',
                        args.image, '-ec', script])

    def deployment(name, password=None):
        folder = work / name
        folder.mkdir()
        (folder / 'music').mkdir(mode=0o755)
        shutil.copyfile(ROOT / 'compose.prod.yaml', folder / 'compose.yaml')
        (folder / '.env').write_text('ROOMUSIC_MUSIC_DIR=./music\n' +
                                    (f'ROOMUSIC_DB_PASSWORD={password}\n' if password else ''))
        override = {
            'services': {
                'init': {'image': args.image, 'cpus': 0.5, 'mem_limit': '128m'},
                'app': {'image': args.image, 'cpus': 2, 'mem_limit': '1g'},
                'postgres': {'cpus': 1, 'mem_limit': '512m'},
            }
        }
        # !override 清除生产默认端口，由 Docker 分配本机可用端口。
        (folder / 'override.json').write_text(json.dumps(override))
        (folder / 'ports.yaml').write_text('services:\n  app:\n    ports: !override\n      - "0.0.0.0::8080"\n')
        return folder

    def compose(folder, name):
        base = ['docker', 'compose', '-p', prefix + '-' + name, '--project-directory', str(folder),
                '--env-file', str(folder / '.env'), '-f', str(folder / 'compose.yaml'),
                '-f', str(folder / 'override.json'), '-f', str(folder / 'ports.yaml')]
        projects.append(base)
        return lambda *cmd, **kwargs: command(base + list(cmd), **kwargs)

    def sql(c, query):
        return c('exec', '-T', 'postgres', 'psql', '-U', 'roomusic', '-d', 'roomusic',
                 '-At', '-v', 'ON_ERROR_STOP=1', input=query).stdout.strip()

    def up(c):
        c('up', '-d', '--wait', '--wait-timeout', '180')
        cid = c('ps', '-q', 'app').stdout.strip()
        info = json.loads(command(['docker', 'inspect', cid]).stdout)[0]
        port = info['NetworkSettings']['Ports']['8080/tcp'][0]
        assert port['HostIp'] == '0.0.0.0'
        assert urllib.request.urlopen('http://127.0.0.1:' + port['HostPort'] + '/readyz').status == 200
        for service in ('app', 'postgres', 'init'):
            cid = c('ps', '-a', '-q', service).stdout.strip()
            info = json.loads(command(['docker', 'inspect', cid]).stdout)[0]
            assert all(m['Type'] == 'bind' for m in info['Mounts']), '出现非目录持久化挂载'
        assert sql(c, 'SELECT count(*) FROM schema_migrations;') != '0'
        c('exec', '-T', 'app', '/bin/sh', '-ec', 'test "$(id -u)" = 1000; touch /data/write-probe /backups/write-probe')

    try:
        folder = deployment('original')
        c = compose(folder, 'original')
        up(c)
        secret = folder / 'secrets/postgres_password'
        digest = hashlib.sha256(secret.read_bytes()).digest()
        assert len(secret.read_bytes()) == 64
        assert secret.stat().st_mode & 0o777 == 0o600
        assert secret.stat().st_uid == 1000
        sql(c, "CREATE TABLE deployment_probe(value text); INSERT INTO deployment_probe VALUES ('before-backup');")
        c('up', '-d', '--force-recreate', '--wait', '--wait-timeout', '180')
        assert hashlib.sha256(secret.read_bytes()).digest() == digest
        assert sql(c, 'SELECT value FROM deployment_probe;') == 'before-backup'
        c('exec', '-T', 'app', '/app/roomusic', 'backup')
        dumps = sorted((folder / 'backups').glob('*manual.dump'))
        assert dumps, '未生成手工备份'
        sql(c, "UPDATE deployment_probe SET value='after-backup';")
        c('stop', 'app')
        c('run', '--rm', '-T', 'app', 'restore', dumps[-1].name, input='roomusic\n')
        assert sql(c, 'SELECT value FROM deployment_probe;') == 'before-backup'
        c('down', '--remove-orphans')
        helper('cp -a /work/original /work/moved')
        moved = work / 'moved'
        c2 = compose(moved, 'moved')
        up(c2)
        assert hashlib.sha256((moved / 'secrets/postgres_password').read_bytes()).digest() == digest
        assert sql(c2, 'SELECT value FROM deployment_probe;') == 'before-backup'
        c2('down', '--remove-orphans')
        (moved / 'secrets/postgres_password').unlink()
        result = c2('run', '--rm', '-T', '--no-deps', 'init', check=False)
        assert result.returncode != 0 and '数据库目录已有内容' in result.stderr
        assert not (moved / 'secrets/postgres_password').exists()
        print('自动密码、重建复用、备份恢复、停机目录搬迁与密码丢失检查通过。', flush=True)

        manual = deployment('manual', 'synthetic:@/?#%&-password')
        cm = compose(manual, 'manual')
        up(cm)
        cm('exec', '-T', 'app', '/app/roomusic', 'backup')
        before = (manual / 'secrets/postgres_password').read_bytes()
        assert before == b'synthetic:@/?#%&-password'
        result = cm('run', '--rm', '-T', '--no-deps', '-e', 'ROOMUSIC_DB_PASSWORD=different-test-value', 'init', check=False)
        assert result.returncode != 0 and '不一致' in result.stderr
        assert (manual / 'secrets/postgres_password').read_bytes() == before
        logs = cm('logs', '--no-color').stdout
        assert before.decode() not in logs
        print('手动特殊字符密码、真实连接与备份、冲突拒绝和日志检查通过。', flush=True)
    finally:
        for base in reversed(projects):
            command(base + ['down', '--remove-orphans'])
        helper('rm -rf /work/original /work/moved /work/manual')
        shutil.rmtree(work)
        print('测试容器、网络及临时部署目录已清理。', flush=True)


if __name__ == '__main__':
    main()
