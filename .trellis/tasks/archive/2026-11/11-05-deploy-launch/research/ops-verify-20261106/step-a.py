from ops import *
assert not any(k.startswith(('ROOMUSIC_','COMPOSE_')) for k in os.environ)
before=json.loads((E/'baseline-before.json').read_text())
assert before['prod_endpoints']['/api/v1/setup/status']['body']=={'setup_required':True}
all_vols=sorted({x['Name'] for a in before['prod_mounts'].values() for x in a if x['Type']=='volume'})
vc={n:out(['docker','volume','inspect','-f','{{.CreatedAt}}',n]) for n in all_vols}
save('prod-all-volume-creation-before.json',vc)
t=time.monotonic()
p=run(P+['restart']); (E/'a1-compose-restart.log').write_text(p.stdout+p.stderr)
a1=wait_healthy('roomusic-prod');a1['total_seconds']=round(time.monotonic()-t,3)
a1['endpoints']={p:get(p) for p in ['/healthz','/readyz','/api/v1/setup/status']}
a1['volumes_unchanged']=vc=={n:out(['docker','volume','inspect','-f','{{.CreatedAt}}',n]) for n in all_vols}
save('a1-result.json',a1)
assert a1['volumes_unchanged'] and a1['endpoints']['/api/v1/setup/status']['body']['setup_required']
old=state('roomusic-prod')['app'];pid=old['pid'];t=time.monotonic();since=str(int(time.time()))
p=run(['kill','-KILL',str(pid)],check=False)
a2={'host_kill_rc':p.returncode,'host_kill_stderr':p.stderr.strip(),'before':old}
if p.returncode:
    p=run(P+['exec','-T','app','/bin/sh','-c','kill -KILL 1'],check=False)
    a2['container_kill_rc']=p.returncode
while time.monotonic()-t<120:
    s=state('roomusic-prod')['app']
    if s['restart_count']>old['restart_count'] and s['health']=='healthy' and s['status']=='running':break
    time.sleep(1)
a2['after']=s;a2['seconds']=round(time.monotonic()-t,3)
a2['passed']=s['restart_count']>old['restart_count'] and s['health']=='healthy' and s['started']!=old['started']
a2['endpoints']={p:get(p) for p in ['/healthz','/readyz','/api/v1/setup/status']}
save('a2-result.json',a2)
# 只读 Docker 事件，无环境或日志负载。
events=run(['docker','events','--since',since,'--until',str(int(time.time())+1),'--filter','container=roomusic-prod-app-1','--format','{{.Time}} {{.Action}} exit={{index .Actor.Attributes "exitCode"}} signal={{index .Actor.Attributes "signal"}}'])
(E/'a2-events.log').write_text(events.stdout)
enabled=run(['systemctl','is-enabled','docker'],check=False)
a3={'classification':'仅配置核对，未重启机器或 Docker','docker_enabled':enabled.stdout.strip(),'rc':enabled.returncode,'containers':state('roomusic-prod')}
save('a3-result.json',a3)
