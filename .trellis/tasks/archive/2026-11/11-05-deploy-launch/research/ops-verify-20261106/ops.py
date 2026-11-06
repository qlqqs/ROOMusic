import subprocess, json, os, time, pathlib, datetime, shlex, urllib.request
E=pathlib.Path('/tmp/roomusic-ops-verify-20261106')
R=pathlib.Path('/home/qlqq/workspace/ROOMusic')
D=['docker','compose','-p','roomusic-drill','-f','deploy/production/compose.yaml','--env-file','/tmp/roomusic-drill/drill.env']
P=['docker','compose','-p','roomusic-prod','-f','deploy/production/compose.yaml','--env-file','.env.prod']
def run(args,input=None,env=None,check=True):
    with (E/'commands.log').open('a') as f: f.write(datetime.datetime.now(datetime.timezone.utc).isoformat()+' '+shlex.join(args)+ (' [stdin未记录]' if input else '')+'\n')
    p=subprocess.run(args,input=input,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,cwd=R,env=env)
    if p.returncode and check: raise RuntimeError(f'命令失败 rc={p.returncode}: {shlex.join(args)}; {p.stderr.strip()}')
    return p

def out(args,**kw):return run(args,**kw).stdout.strip()
def save(name,data):
    (E/name).write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(data,ensure_ascii=False,indent=2),flush=True)
def state(project):
    result={}
    for service in ['app','postgres','backup']:
        n=f'{project}-{service}-1'
        fmt='{{json .State}}'
        # 仅状态数据，不含 Config.Env、请求或日志。
        s=json.loads(out(['docker','inspect','-f',fmt,n]))
        result[service]={'status':s['Status'],'started':s['StartedAt'],'pid':s['Pid'],'health':s.get('Health',{}).get('Status'),'restart_count':int(out(['docker','inspect','-f','{{.RestartCount}}',n])),'policy':out(['docker','inspect','-f','{{.HostConfig.RestartPolicy.Name}}',n])}
    return result

def wait_healthy(project,timeout=320):
    start=time.monotonic()
    while time.monotonic()-start<timeout:
        s=state(project)
        if all(x['status']=='running' and x['health']=='healthy' for x in s.values()):return {'seconds':round(time.monotonic()-start,3),'state':s}
        time.sleep(2)
    raise RuntimeError('健康等待超时')
def get(path,port=8090):
    # 不继承代理；正式实例仅 GET。
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(f'http://127.0.0.1:{port}'+path,timeout=20) as r:return {'status':r.status,'body':json.loads(r.read())}
def volumes(project):
    names=out(['docker','volume','ls','-q','--filter',f'label=com.docker.compose.project={project}']).splitlines()
    return {n:json.loads(out(['docker','volume','inspect','-f','{{json .}}',n])) for n in names}
def baseline():
    dev={
      'state':out(['docker','inspect','-f','{{.State.Status}} {{.State.StartedAt}}','roomusic-postgres-1']),
      'volume_created':out(['docker','volume','inspect','-f','{{.CreatedAt}}','roomusic_postgres-data']),
      'counts':out(['docker','exec','roomusic-postgres-1','sh','-c','exec psql -X -qAt -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c \'BEGIN TRANSACTION READ ONLY; select max(version) from schema_migrations; select count(*) from users; select count(*) from releases; COMMIT;\'']).splitlines()}
    files=[]
    for p in sorted(pathlib.Path('/home/qlqq/roomusic-backups').iterdir()):
        st=p.stat(); entry={'name':p.name,'size':st.st_size,'mtime_ns':st.st_mtime_ns,'mode':oct(st.st_mode&0o777),'uid':st.st_uid,'gid':st.st_gid}
        if p.is_file():entry['sha256']=out(['sha256sum',str(p)]).split()[0]
        files.append(entry)
    return {'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'dev':dev,'prod_state':state('roomusic-prod'),'prod_volumes':volumes('roomusic-prod'),'prod_mounts':{s:json.loads(out(['docker','inspect','-f','{{json .Mounts}}',f'roomusic-prod-{s}-1'])) for s in ['app','postgres','backup']},'prod_endpoints':{p:get(p) for p in ['/healthz','/readyz','/api/v1/setup/status']},'prod_backup_files':files,'git_head':out(['git','rev-parse','HEAD']),'git_status':out(['git','status','--porcelain'])}
if __name__=='__main__':
    assert not any(k.startswith(('ROOMUSIC_','COMPOSE_')) for k in os.environ)
    save('baseline-before.json',baseline())
