#!/usr/bin/env python3
"""正式启动前后仅采集开发库允许的只读聚合；不覆盖演练基线。"""
import datetime
import json
from pathlib import Path
import shlex
import subprocess
import sys

HERE = Path(__file__).resolve().parent


def run(*args):
    print('+ ' + shlex.join(args), flush=True)
    return subprocess.check_output(args, text=True).strip()


def main():
    phase = sys.argv[1]
    assert phase in ('baseline', 'after')
    config = dict(line.split('=', 1) for line in run('docker', 'inspect', '-f', '{{range .Config.Env}}{{if or (eq (index (split . "=") 0) "POSTGRES_USER") (eq (index (split . "=") 0) "POSTGRES_DB")}}{{println .}}{{end}}{{end}}', 'roomusic-postgres-1').splitlines() if line)
    assert set(config) == {'POSTGRES_USER', 'POSTGRES_DB'}
    state = run('docker', 'inspect', '-f', '{{.State.Status}} {{.State.StartedAt}}', 'roomusic-postgres-1')
    created = run('docker', 'volume', 'inspect', '-f', '{{.CreatedAt}}', 'roomusic_postgres-data')
    sql = 'select max(version) from schema_migrations; select count(*) from users; select count(*) from releases'
    counts = run('docker', 'exec', 'roomusic-postgres-1', 'psql', '-U', config['POSTGRES_USER'], '-d', config['POSTGRES_DB'], '-Atc', sql).splitlines()
    assert len(counts) == 3
    result = dict(utc=datetime.datetime.now(datetime.timezone.utc).isoformat(), state=state, volume_created=created, counts=dict(schema_max_version=counts[0], users=counts[1], releases=counts[2]))
    if phase == 'after':
        baseline = json.loads((HERE / 'dev-baseline.json').read_text())
        result['comparison'] = {key: baseline[key] == result[key] for key in ('state', 'volume_created', 'counts')}
    output = HERE / f'dev-{phase}.json'
    assert not output.exists()
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
    assert state.startswith('running ') and counts == ['12', '1', '68']
    if phase == 'after':
        assert all(result['comparison'].values()), '开发库前后不一致，停止。'
    print('通过：仅按允许范围读取开发库状态、卷创建时间及三项聚合。')


if __name__ == '__main__':
    main()
