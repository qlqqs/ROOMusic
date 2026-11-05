#!/usr/bin/env python3
"""仅清理 roomusic-drill；每个破坏性操作前核对精确归属，记录不含秘密。"""
import datetime
import json
import os
import re
from pathlib import Path
import shlex
import subprocess

ROOT = Path('/home/qlqq/workspace/ROOMusic')
EVIDENCE = Path(__file__).resolve().parent
TMP = Path('/tmp/roomusic-drill')
PROJECT = 'roomusic-drill'
ENV = {k: v for k, v in os.environ.items() if not k.startswith(('ROOMUSIC_', 'COMPOSE_'))}
D = ['docker', 'compose', '-p', PROJECT, '-f', 'deploy/production/compose.yaml', '--env-file', str(TMP / 'drill.env')]
SECRETS = set()


def log(message):
    print(message, flush=True)


def command(args, *, echo=True, display=True):
    if echo:
        log('+ ' + shlex.join(args))
    proc = subprocess.run(args, cwd=ROOT, env=ENV, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    output = proc.stdout + proc.stderr
    for value in SECRETS:
        output = output.replace(value, '[已脱敏]')
    if display and output.strip():
        log(output.rstrip())
    if proc.returncode:
        if not display:
            log('命令失败；不打印可能含配置值的输出。')
        raise RuntimeError(f'命令退出 {proc.returncode}，停止清理。')
    return proc.stdout.strip()


def inspect_container(name):
    template = '{{json .Name}}\n{{json .Config.Labels}}\n{{json .Mounts}}'
    rows = command(['docker', 'inspect', '-f', template, name], display=False).splitlines()
    return [json.loads(row) for row in rows]


def main():
    assert Path.cwd() == ROOT
    assert PROJECT == 'roomusic-drill' and D[2:4] == ['-p', 'roomusic-drill']
    assert TMP.is_dir() and not TMP.is_symlink() and TMP.resolve() == TMP
    log('第 13 步：用户决定停止演练；第 9–12 步用户决定跳过（2026-11-05）。')
    log('UTC：' + datetime.datetime.now(datetime.timezone.utc).isoformat())
    # 仅在内存读取演练秘密，不输出配置、秘密或其散列；不读取 .env/.env.dev。
    for line in (TMP / 'drill.env').read_text().splitlines():
        if line.startswith('ROOMUSIC_DB_PASSWORD='):
            SECRETS.add(line.split('=', 1)[1].strip().strip('\"\''))
    SECRETS.add(json.loads((TMP / 'admin.json').read_text())['password'])
    for state in TMP.glob('*state.json'):
        for cookie in json.loads(state.read_text()).get('cookies', []):
            if cookie.get('value'):
                SECRETS.add(cookie['value'])
    assert len(SECRETS) >= 2 and all(len(value) >= 16 for value in SECRETS)
    files = [p for p in EVIDENCE.rglob('*') if p.is_file()]
    leaks = [str(p.relative_to(EVIDENCE)) for p in files if any(value.encode() in p.read_bytes() for value in SECRETS)]
    if leaks:
        raise RuntimeError('记录包含秘密，停止：' + ', '.join(leaks))
    log(f'秘密扫描通过：{len(files)} 个记录/脚本/图片文件未包含已知演练数据库密码、管理员密码或会话值；不输出秘密。')
    # 配置完整输出只在内存检查，绝不落盘。
    cfg = json.loads(command(D + ['config', '--format', 'json'], display=False))
    assert cfg['name'] == PROJECT
    assert {v['name'] for v in cfg['volumes'].values()} == {'roomusic-drill_app-data', 'roomusic-drill_postgres-data'}
    assert all(not v.get('external') for v in cfg['volumes'].values())
    assert cfg['networks']['roomusic']['name'] == 'roomusic-drill_roomusic'
    assert cfg['networks']['roomusic']['ipam']['config'][0]['subnet'] == '10.203.51.0/24'
    assert cfg['services']['app']['image'].startswith('roomusic-drill-app:')
    assert cfg['services']['backup']['image'].startswith('roomusic-drill-tools:')
    log('配置白名单核对通过：name=roomusic-drill，两个专属命名卷，网络 roomusic-drill_roomusic/10.203.51.0/24；未输出完整配置。')
    names = command(['docker', 'ps', '-a', '--format', '{{.Names}}'], display=False).splitlines()
    if 'roomusic-drill-capture' in names:
        name, labels, mounts = inspect_container('roomusic-drill-capture')
        assert name == '/roomusic-drill-capture'
        assert all(m.get('Type') != 'volume' for m in mounts)
        command(['docker', 'stop', 'roomusic-drill-capture'])
        # --rm 容器停止后自动删除；否则仅删精确名字。
        remaining = command(['docker', 'ps', '-a', '--format', '{{.Names}}'], display=False).splitlines()
        if 'roomusic-drill-capture' in remaining:
            command(['docker', 'rm', 'roomusic-drill-capture'])
    else:
        log('抓包已捕获两次后自动退出；roomusic-drill-capture 不存在，无需停止。')
    owned_anonymous = []
    for name in names:
        if name.startswith('roomusic-drill-') and name not in {'roomusic-drill-capture', 'roomusic-drill-tls'}:
            actual, labels, mounts = inspect_container(name)
            assert actual == '/' + name and labels.get('com.docker.compose.project') == PROJECT
            for mount in mounts:
                if mount['Type'] != 'volume' or mount['Name'].startswith('roomusic-drill_'):
                    continue
                # tools 继承 PostgreSQL 镜像的 VOLUME；只接受 backup 独占的匿名卷。
                volume = mount['Name']
                assert name == 'roomusic-drill-backup-1'
                assert mount['Destination'] == '/var/lib/postgresql' and re.fullmatch(r'[0-9a-f]{64}', volume)
                users = command(['docker', 'ps', '-a', '--filter', 'volume=' + volume, '--format', '{{.Names}}'])
                assert users.splitlines() == [name]
                owned_anonymous.append(volume)
                log(f'匿名卷归属确认：{volume} 仅由 roomusic-drill-backup-1 使用。')
            log(f'容器归属确认：{name} project=roomusic-drill；卷均为演练专属。')
    for name in ['roomusic-drill_app-data', 'roomusic-drill_postgres-data']:
        actual = command(['docker', 'volume', 'inspect', '-f', '{{.Name}} {{index .Labels "com.docker.compose.project"}}', name])
        assert actual == name + ' ' + PROJECT
    log('破坏性操作前最终确认：下一条 Compose 命令包含 -p roomusic-drill，仅删除演练实例及其卷。')
    assert D[D.index('-p') + 1] == 'roomusic-drill'
    command(D + ['down', '-v'])
    if 'roomusic-drill-tls' in names:
        actual, labels, mounts = inspect_container('roomusic-drill-tls')
        assert actual == '/roomusic-drill-tls'
        assert all(m['Type'] == 'bind' and m['Source'] == str(TMP / 'Caddyfile') for m in mounts)
        log('TLS 专属容器确认：仅绑定演练 Caddyfile，没有命名卷或匿名卷。')
        command(['docker', 'stop', 'roomusic-drill-tls'])
        command(['docker', 'rm', 'roomusic-drill-tls'])
    tags = command(['docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}'], display=False).splitlines()
    tags = sorted({tag for tag in tags if tag.startswith('roomusic-drill-')})
    assert all(tag.split(':', 1)[0] in {'roomusic-drill-app', 'roomusic-drill-tools', 'roomusic-drill-c6-app', 'roomusic-drill-c6-tools'} for tag in tags)
    if tags:
        log('镜像删除前确认：以下标签全部属于 roomusic-drill-*，不按 ID、不强制删除。')
        command(['docker', 'image', 'rm', *tags])
    assert not (TMP / 'clone').exists()
    log('临时 clone 已在第 1b 步删除；再次确认不存在。')
    assert TMP.resolve() == Path('/tmp/roomusic-drill') and not TMP.is_symlink()
    # 不允许目录中存在挂载点；不沿符号链接删除目标。
    assert not any(p.is_mount() for p in [TMP, *TMP.rglob('*')] if not p.is_symlink())
    log('临时路径确认：精确 /tmp/roomusic-drill，非符号链接、无挂载点；保留仓库 research/drill。')
    command(['rm', '-r', '--', str(TMP)])
    checks = {}
    for kind, args in {
        'containers': ['docker', 'ps', '-a', '--format', '{{.Names}}'],
        'volumes': ['docker', 'volume', 'ls', '--format', '{{.Name}}'],
        'networks': ['docker', 'network', 'ls', '--format', '{{.Name}}'],
        'images': ['docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}'],
    }.items():
        values = command(args, display=False).splitlines()
        checks[kind] = [value for value in values if 'roomusic-drill' in value]
        assert not checks[kind], checks[kind]
        log(f'清理核对 {kind}：roomusic-drill/c6/tls/capture 残留 0。')
    for volume in owned_anonymous:
        existing = command(['docker', 'volume', 'ls', '--format', '{{.Name}}'], display=False).splitlines()
        assert volume not in existing
        log(f'演练 backup 独占匿名卷已随 down -v 删除：{volume}')
    checks['owned_anonymous_volumes_removed'] = True
    listeners = command(['ss', '-H', '-lnt', '( sport = :18090 or sport = :18443 )'])
    assert not listeners
    assert not TMP.exists() and EVIDENCE.is_dir()
    checks.update(ports_18090_18443_free=True, tmp_removed=True, clone_removed=True, evidence_retained=True, known_secrets_scan_passed=True)
    result = {'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'project': PROJECT, 'checks': checks, 'result': 'passed'}
    (EVIDENCE / 'step-13-result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    log('结果：清理通过；开发资源未执行任何停止、重启、删除或写入命令。')


if __name__ == '__main__':
    main()
