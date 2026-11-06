from ops import *
import secrets, shutil
assert not any(k.startswith(('ROOMUSIC_','COMPOSE_')) for k in os.environ)
b=json.loads((E/'baseline-before.json').read_text())
latest=max((x for x in b['prod_backup_files'] if x['name'].endswith('.dump')),key=lambda x:x['mtime_ns'])['name']
t=time.monotonic();p=run(['deploy/production/restore.sh','check','/home/qlqq/roomusic-backups/'+latest],check=False)
(E/'b1-restore-check.log').write_text(p.stdout+p.stderr)
save('b1-result.json',{'rc':p.returncode,'seconds':round(time.monotonic()-t,3),'backup':latest,'output':p.stdout})
assert p.returncode==0
assert not out(['docker','ps','-aq','--filter','label=com.docker.compose.project=roomusic-drill'])
assert not out(['docker','volume','ls','-q','--filter','label=com.docker.compose.project=roomusic-drill'])
# 核对端口和子网，禁止因复用既有资源而影响其他实例。
import socket,ipaddress
s=socket.socket();s.bind(('0.0.0.0',18090));s.close()
for n in out(['docker','network','ls','-q']).splitlines():
    for cfg in json.loads(out(['docker','network','inspect','-f','{{json .IPAM.Config}}',n])) or []:
        if cfg.get('Subnet'):assert not ipaddress.ip_network('10.203.51.0/24').overlaps(ipaddress.ip_network(cfg['Subnet'])), '子网冲突'
tmp=pathlib.Path('/tmp/roomusic-drill');tmp.mkdir(mode=0o700);(tmp/'backups').mkdir(mode=0o700)
shutil.copyfile(R/'.env.prod.example',tmp/'drill.env');os.chmod(tmp/'drill.env',0o600)
text=(tmp/'drill.env').read_text()
values={'ROOMUSIC_DB_PASSWORD':secrets.token_hex(24),'ROOMUSIC_HTTP_PORT':'18090','ROOMUSIC_MUSIC_DIR':str(R/'music'),'ROOMUSIC_BACKUP_DIR':str(tmp/'backups'),'ROOMUSIC_SUBNET':'10.203.51.0/24'}
for k,v in values.items():
    import re
    text,count=re.subn('^'+k+'=.*$',k+'='+v,text,flags=re.M);assert count==1
text+='\nROOMUSIC_IMAGE=roomusic-prod-app:06450d0ce2d7\nROOMUSIC_TOOLS_IMAGE=roomusic-prod-tools:06450d0ce2d7\n'
(tmp/'drill.env').write_text(text)
(tmp/'admin.json').write_text(json.dumps({'username':'ops-drill-admin','password':secrets.token_hex(24)}));os.chmod(tmp/'admin.json',0o600)
images=out(['docker','image','ls','--no-trunc','--format','{{.Repository}}:{{.Tag}} {{.ID}}','--filter','reference=roomusic-prod-*'])
(E/'prod-images-before.txt').write_text(images+'\n')
run(D+['config','--quiet'])
t=time.monotonic();p=run(D+['up','-d','--wait','--wait-timeout','300'],check=False)
(E/'b2-start.log').write_text(p.stdout+p.stderr)
save('b2-start-result.json',{'rc':p.returncode,'seconds':round(time.monotonic()-t,3),'state':state('roomusic-drill')})
assert p.returncode==0
mounts=json.loads(out(['docker','inspect','-f','{{json .Mounts}}','roomusic-drill-app-1']))
assert any(x['Source']==str(R/'music') and x['Destination']=='/music' and not x['RW'] for x in mounts)
save('drill-isolation.json',{'app_mounts':mounts,'network':json.loads(out(['docker','network','inspect','-f','{{json .IPAM.Config}}','roomusic-drill_roomusic'])),'ports':out(['docker','port','roomusic-drill-app-1']),'env_mode':oct((tmp/'drill.env').stat().st_mode&0o777),'backup_uid':(tmp/'backups').stat().st_uid,'images':{x:out(['docker','inspect','-f','{{.Config.Image}}',f'roomusic-drill-{x}-1']) for x in ['app','backup']}})
