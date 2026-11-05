#!/usr/bin/env python3
"""仅记录开发库允许读取的基线；不读取或输出密码。"""
import datetime
import json
import os
import subprocess
from pathlib import Path


def run(*args):
    return subprocess.check_output(args, text=True).strip()


assert run('git', 'branch', '--show-current') == 'main'
assert run('git', 'rev-parse', 'HEAD') == '263d75709b2b2fe8fc0ed79e8f7ef824eb3dba47'
assert not any(k.startswith(('ROOMUSIC_', 'COMPOSE_')) for k in os.environ)
# 在 Docker 内按白名单过滤，仅返回用户名/库名，不读取完整 inspect 输出。
fields = run('docker', 'inspect', '-f', '{{range .Config.Env}}{{if or (eq (index (split . "=") 0) "POSTGRES_USER") (eq (index (split . "=") 0) "POSTGRES_DB")}}{{println .}}{{end}}{{end}}', 'roomusic-postgres-1')
config = dict(line.split('=', 1) for line in fields.splitlines() if line)
assert set(config) == {'POSTGRES_USER', 'POSTGRES_DB'}
state = run('docker', 'inspect', '-f', '{{.State.Status}} {{.State.StartedAt}}', 'roomusic-postgres-1')
volume_created = run('docker', 'volume', 'inspect', '-f', '{{.CreatedAt}}', 'roomusic_postgres-data')
sql = 'select max(version) from schema_migrations; select count(*) from users; select count(*) from releases'
counts = run('docker', 'exec', 'roomusic-postgres-1', 'psql', '-U', config['POSTGRES_USER'], '-d', config['POSTGRES_DB'], '-Atc', sql).splitlines()
assert state.startswith('running '), state
assert counts == ['12', '1', '68'], counts
result = dict(utc=datetime.datetime.now(datetime.timezone.utc).isoformat(), state=state, volume_created=volume_created, counts=dict(schema_max_version=counts[0], users=counts[1], releases=counts[2]))
Path(__file__).with_name('step-00-baseline.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print('命令：python3 .trellis/tasks/11-05-deploy-packaging/research/drill/step-00.py')
print('执行：docker inspect（仅状态、启动时间、POSTGRES_USER、POSTGRES_DB）')
print('执行：docker volume inspect（仅卷创建时间）')
print('执行：docker exec roomusic-postgres-1 psql（只读上述三项聚合 SQL）')
print(json.dumps(result, ensure_ascii=False, indent=2))
print('ROOMUSIC_* / COMPOSE_* 环境残留：无')
print('结果：通过；开发库仅执行只读查询。')
