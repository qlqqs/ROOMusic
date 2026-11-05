#!/usr/bin/env python3
"""重复步骤 0 的三类只读检查；保留原基线，另存结果和逐项对比。"""
import datetime
import json
from pathlib import Path
import shlex
import subprocess

HERE = Path(__file__).resolve().parent


def run(*args):
    print('+ ' + shlex.join(args), flush=True)
    return subprocess.check_output(args, text=True).strip()


def main():
    baseline = json.loads((HERE / 'step-00-baseline.json').read_text())
    template = '{{range .Config.Env}}{{if or (eq (index (split . "=") 0) "POSTGRES_USER") (eq (index (split . "=") 0) "POSTGRES_DB")}}{{println .}}{{end}}{{end}}'
    fields = run('docker', 'inspect', '-f', template, 'roomusic-postgres-1')
    config = dict(line.split('=', 1) for line in fields.splitlines() if line)
    assert set(config) == {'POSTGRES_USER', 'POSTGRES_DB'}
    state = run('docker', 'inspect', '-f', '{{.State.Status}} {{.State.StartedAt}}', 'roomusic-postgres-1')
    created = run('docker', 'volume', 'inspect', '-f', '{{.CreatedAt}}', 'roomusic_postgres-data')
    sql = 'select max(version) from schema_migrations; select count(*) from users; select count(*) from releases'
    counts = run('docker', 'exec', 'roomusic-postgres-1', 'psql', '-U', config['POSTGRES_USER'], '-d', config['POSTGRES_DB'], '-Atc', sql).splitlines()
    assert len(counts) == 3
    current = dict(utc=datetime.datetime.now(datetime.timezone.utc).isoformat(), state=state, volume_created=created, counts=dict(schema_max_version=counts[0], users=counts[1], releases=counts[2]))
    comparison = {key: {'baseline': baseline[key], 'current': current[key], 'unchanged': baseline[key] == current[key]} for key in ('state', 'volume_created', 'counts')}
    passed = state.startswith('running ') and all(item['unchanged'] for item in comparison.values())
    result = dict(current=current, comparison=comparison, result='passed' if passed else 'failed')
    (HERE / 'step-14-result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2), flush=True)
    assert passed, '开发库基线对比失败；停止并报告，不执行修复。'
    print('结果：通过；状态/启动时间、卷创建时间、三项计数全部不变。仅执行允许的只读检查。')


if __name__ == '__main__':
    main()
