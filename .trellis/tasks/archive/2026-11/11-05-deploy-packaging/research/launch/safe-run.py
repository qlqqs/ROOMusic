#!/usr/bin/env python3
"""生产验收命令包装：记录命令与脱敏输出；不保存原始含秘密输出。"""
import os
from pathlib import Path
import shlex
import subprocess
import sys

ROOT = Path('/home/qlqq/workspace/ROOMusic')
DEST = Path(__file__).resolve().parent


def main():
    filename, *args = sys.argv[1:]
    assert Path.cwd() == ROOT and Path(filename).name == filename and filename.endswith('.log')
    assert args
    secrets = []
    config = ROOT / '.env.prod'
    if config.exists():
        for line in config.read_text().splitlines():
            if line.startswith('ROOMUSIC_DB_PASSWORD='):
                value = line.split('=', 1)[1]
                assert len(value) == 48
                secrets.append(value)
    env = {key: value for key, value in os.environ.items() if not key.startswith(('ROOMUSIC_', 'COMPOSE_', 'MAKE')) and key != 'REF'}
    env['BUILDKIT_PROGRESS'] = 'plain'
    with (DEST / filename).open('x') as log:
        def emit(text):
            for secret in secrets:
                text = text.replace(secret, '[已脱敏]')
            log.write(text)
            log.flush()
            print(text, end='', flush=True)
        emit('+ ' + shlex.join(args) + '\n')
        process = subprocess.Popen(args, cwd=ROOT, env=env, text=True, errors='replace', stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        for line in process.stdout:
            emit(line)
        code = process.wait()
        emit(f'退出码：{code}\n')
    sys.exit(code)


if __name__ == '__main__':
    main()
