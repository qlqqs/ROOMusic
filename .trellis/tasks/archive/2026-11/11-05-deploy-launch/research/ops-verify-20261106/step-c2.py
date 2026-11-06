from drilldb import *
name='roomusic-drill_app-data';project='roomusic-drill'
assert project=='roomusic-drill' and name=='roomusic-drill_app-data'
before=json.loads(out(['docker','volume','inspect','-f','{{json .}}',name]))
assert before['Name']==name and before['Labels']['com.docker.compose.project']==project and before['Labels']['com.docker.compose.volume']=='app-data'
save('c2-volume-delete-precheck.json',before)
db_before={'started':state(project)['postgres']['started'],'created':out(['docker','volume','inspect','-f','{{.CreatedAt}}','roomusic-drill_postgres-data']),'counts':counts()}
t=time.monotonic()
p=run(D+['stop','app']);(E/'c2-stop.log').write_text(p.stdout+p.stderr)
# Docker 不允许删除仍被停止容器引用的卷；仅移除本演练的 app 容器。
p=run(D+['rm','-f','app']);(E/'c2-rm-app-container.log').write_text(p.stdout+p.stderr)
assert not out(['docker','ps','-aq','--filter','volume='+name])
verified=json.loads(out(['docker','volume','inspect','-f','{{json .}}',name]))
assert verified==before
run(['docker','volume','rm',name])
p=run(D+['up','-d','--no-deps','--wait','--wait-timeout','300','app']);(E/'c2-up.log').write_text(p.stdout+p.stderr)
new=json.loads(out(['docker','volume','inspect','-f','{{json .}}',name]))
db_after={'started':state(project)['postgres']['started'],'created':out(['docker','volume','inspect','-f','{{.CreatedAt}}','roomusic-drill_postgres-data']),'counts':counts()}
save('c2-result.json',{'seconds':round(time.monotonic()-t,3),'old_volume_created':before['CreatedAt'],'new_volume_created':new['CreatedAt'],'volume_recreated':new['CreatedAt']!=before['CreatedAt'],'database_unchanged':db_before==db_after,'state':state(project),'healthz':get('/healthz',18090),'readyz':get('/readyz',18090)})
assert db_before==db_after
save('c2-artwork-db-before-rescan.json',artwork_snapshot('artwork-post-loss.json'))
