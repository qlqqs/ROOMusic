#!/usr/bin/env python3
"""仅从正式实例读取状态白名单，不读取完整容器环境或配置。"""
import datetime
import json
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent


def run(*args):
    return subprocess.check_output(args, text=True).strip()


phase = sys.argv[1]
assert phase in ('first', 'final')
expected = run('git', 'rev-parse', 'main')
assert expected == '06450d0ce2d721e01e412f74ea3927b8d256ffe1'
short = run('git', 'rev-parse', '--short=12', 'main')
template = '{"name":{{json .Name}},"image":{{json .Config.Image}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"status":{{json .State.Status}},"health":{{json .State.Health.Status}},"revision":{{json (index .Config.Labels "org.opencontainers.image.revision")}},"restart":{{json .HostConfig.RestartPolicy.Name}},"ports":{{json .NetworkSettings.Ports}},"mounts":{{json .Mounts}}}'
services = {}
for service in ('app', 'postgres', 'backup'):
    item = json.loads(run('docker', 'inspect', '-f', template, f'roomusic-prod-{service}-1'))
    assert item['project'] == 'roomusic-prod' and item['status'] == 'running' and item['health'] == 'healthy'
    assert item['restart'] == 'unless-stopped'
    assert all(mount['Name'].startswith('roomusic-prod_') or (service == 'backup' and mount['Destination'] == '/var/lib/postgresql') for mount in item['mounts'] if mount['Type'] == 'volume')
    if service == 'app':
        assert item['revision'] == expected and item['image'] == f'roomusic-prod-app:{short}'
        assert item['ports']['8080/tcp'] == [{'HostIp': '0.0.0.0', 'HostPort': '8090'}]
        assert any(mount['Destination'] == '/music' and mount['Source'] == '/home/qlqq/workspace/ROOMusic/music' and not mount['RW'] for mount in item['mounts'])
    else:
        assert not any(item['ports'].values())
    if service == 'backup':
        assert item['image'] == f'roomusic-prod-tools:{short}'
    services[service] = item
fields = run('docker', 'inspect', '-f', '{{range .Config.Env}}{{if or (eq (index (split . "=") 0) "ROOMUSIC_ENV") (eq (index (split . "=") 0) "ROOMUSIC_ALLOWED_ORIGINS")}}{{println .}}{{end}}{{end}}', 'roomusic-prod-app-1')
settings = dict(line.split('=', 1) for line in fields.splitlines() if line)
assert settings == {'ROOMUSIC_ENV': 'production', 'ROOMUSIC_ALLOWED_ORIGINS': ''}
subnet = run('docker', 'network', 'inspect', '-f', '{{range .IPAM.Config}}{{.Subnet}}{{end}}', 'roomusic-prod_roomusic')
assert subnet == '10.203.50.0/24'
files = sorted(Path('/home/qlqq/roomusic-backups').glob('roomusic-*.dump'))
assert files
backups = []
for path in files:
    st = path.stat()
    assert st.st_size > 0 and st.st_mode & 0o777 == 0o600 and st.st_uid == 1000
    backups.append(dict(file=path.name, bytes=st.st_size, mode='600', uid=st.st_uid))
result = dict(utc=datetime.datetime.now(datetime.timezone.utc).isoformat(), revision=expected, services=services, origin_mode='same-host', subnet=subnet, backups=backups)
(HERE / f'prod-{phase}.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(result, ensure_ascii=False, indent=2))
print('通过：app/postgres/backup 健康、版本等于 main；仅 IPv4 8090，数据库无发布端口；正式卷隔离，音乐只读；生产清单为空；备份存在且权限 600。')
