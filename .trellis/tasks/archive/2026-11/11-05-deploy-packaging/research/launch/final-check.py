#!/usr/bin/env python3
"""最终仅检查验收证据、Git 边界与配置权限；秘密只在内存比较。"""
import json
from pathlib import Path
import re
import subprocess

HERE = Path(__file__).resolve().parent
NOTE = HERE.with_name('launch.md')
ROOT = Path('/home/qlqq/workspace/ROOMusic')
config = dict(line.split('=', 1) for line in (ROOT / '.env.prod').read_text().splitlines() if line and not line.startswith('#'))
secret = config['ROOMUSIC_DB_PASSWORD']
assert re.fullmatch('[0-9a-f]{48}', secret)
assert (ROOT / '.env.prod').stat().st_mode & 0o777 == 0o600
assert 'ROOMUSIC_ALLOWED_ORIGINS' not in config
subprocess.run(['git', 'check-ignore', '-q', '.env.prod'], check=True)
assert not subprocess.check_output(['git', 'status', '--short', '--untracked-files=all', '--', '.env.prod'], text=True).strip()
files = [NOTE, *[path for path in HERE.rglob('*') if path.is_file()]]
assert all(secret.encode() not in path.read_bytes() for path in files), '证据含敏感值，停止；不输出值。'
assert not list(HERE.rglob('*.dump')) and not list(HERE.rglob('*.pcap'))
print(f'通过：{len(files)} 个证据/脚本/截图文件未发现生产数据库密码；记录无配置副本、备份数据或原始包。')
for path in files:
    if path.suffix == '.py':
        compile(path.read_text(), str(path), 'exec')
    if path.suffix in ('.py', '.cjs', '.md'):
        assert all(line == line.rstrip() for line in path.read_text().splitlines()), path.name
print('通过：新增 Python 脚本内存语法检查及脚本/Markdown 末尾空白检查。')
before = json.loads((HERE / 'dev-baseline.json').read_text())
after = json.loads((HERE / 'dev-after.json').read_text())
assert all(before[key] == after[key] for key in ('state', 'volume_created', 'counts'))
setup = json.loads((HERE / 'setup-result.json').read_text())
assert setup['setup_required'] and setup['writes_sent'] == 0 and not setup['blocked_writes']
smoke = json.loads((HERE / 'smoke-result.json').read_text())
assert smoke['result'] == 'passed' and smoke['login_requests_sent'] == 2 and smoke['initialization_still_required']
assert [row['status'] for row in smoke['responses']] == [401, 403]
final = json.loads((HERE / 'prod-final.json').read_text())
assert final['revision'] == '06450d0ce2d721e01e412f74ea3927b8d256ffe1'
assert all(row['health'] == 'healthy' for row in final['services'].values())
assert len(final['backups']) == 3
print('通过：开发库与基线相同，初始化未执行，恰好两次 probe，正式服务全部健康，三份备份留存。')
print('+ node --check ' + str(HERE.relative_to(ROOT) / 'check-setup.cjs'))
subprocess.run(['node', '--check', str(HERE / 'check-setup.cjs')], check=True)
print('+ git diff --check')
subprocess.run(['git', 'diff', '--check'], check=True)
print('+ git diff --exit-code -- deploy/production scripts backend frontend')
subprocess.run(['git', 'diff', '--exit-code', '--', 'deploy/production', 'scripts', 'backend', 'frontend'], check=True)
assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip() == final['revision']
print('通过：HEAD 未变，未改部署或应用代码；.env.prod 已忽略且权限 600。未运行全量测试，未提交 Git。')
