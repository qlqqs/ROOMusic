#!/usr/bin/env python3
"""只检查已保存的 dry-run 命令，不执行 dry-run 打印内容。"""
from pathlib import Path
import shlex

HERE = Path(__file__).resolve().parent


def commands(filename):
    # 首行是包装器记录的脚本命令，其余 + 行才是脚本打印的内部命令。
    return [shlex.split(line[2:]) for line in (HERE / filename).read_text().splitlines()[1:] if line.startswith('+ ')]


stop = commands('step-03-dry-stop.log')
up = commands('step-03-dry-up.log')
all_commands = stop + up
assert all('down' not in args and '-v' not in args for args in all_commands)

def compose_actions(rows):
    actions = []
    for args in rows:
        if args[:2] != ['docker', 'compose']:
            continue
        assert args[args.index('-p') + 1] == 'roomusic-prod'
        actions.append(args[args.index('--env-file') + 2:])
    return actions

assert compose_actions(stop) == [['stop']]
assert compose_actions(up) == [
    ['config', '--quiet'],
    ['up', '-d', '--wait', 'postgres'],
    ['run', '--rm', '--no-deps', '-T', 'backup', 'now'],
    ['up', '-d', '--wait', '--wait-timeout', '300'],
    ['ps'],
]
assert any(args[:2] == ['docker', 'build'] for args in up)
print('通过：全部 Compose 命令只针对 -p roomusic-prod，无 down/-v。')
print('通过：构建 → config --quiet → 启动数据库 → 升级前备份 → 整体启动 → ps，顺序正确。')
print('说明：首次内联检查误漏最后一条 ps 导致行数断言失败；现按动作列表检查，不修改部署代码。')
