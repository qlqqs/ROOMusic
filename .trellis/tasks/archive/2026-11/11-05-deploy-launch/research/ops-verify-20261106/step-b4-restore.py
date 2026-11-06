from drilldb import *
mutated=counts();save('b4-mutated-db.json',mutated)
assert mutated['favorite_albums']=='1' and mutated['personal_playlists']=='0' and mutated['personal_playlist_items']=='0'
backup=json.loads((T/'chosen-backup.json').read_text())['file'];assert backup.startswith('/tmp/roomusic-drill/backups/')
project='roomusic-drill';assert project=='roomusic-drill'
env=dict(os.environ,COMPOSE_PROJECT_NAME=project,COMPOSE_ENV_FILES=str(T/'drill.env'))
old={p.name for p in (T/'backups').iterdir()}
with (E/'commands.log').open('a') as f:f.write('恢复选择器：COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env；确认输入 roomusic-drill\n')
t=time.monotonic();p=run(['deploy/production/restore.sh','production',backup],env=env,input='roomusic-drill\n',check=False)
(E/'b4-restore-production.log').write_text(p.stdout+p.stderr)
new=sorted(p.name for p in (T/'backups').iterdir() if p.name not in old)
result={'rc':p.returncode,'seconds':round(time.monotonic()-t,3),'safety_backup_files':new,'state':state(project),'counts_after':counts()}
save('b4-restore-result.json',result)
assert p.returncode==0 and len(new)==1 and new[0].endswith('-manual.dump')
assert result['counts_after']['favorite_albums']=='2' and result['counts_after']['personal_playlists']=='1' and result['counts_after']['personal_playlist_items']=='3'
# 将安全备份恢复到独立临时库，只读核对它确实保存了删歌单后的状态。
# 标准 check 不含个人数据计数；先验证流程，个人计数由 _inside 的独立只读临时核对补充。
p=run(['deploy/production/restore.sh','check',str(T/'backups'/new[0])],env=env)
(E/'b4-safety-restore-check.log').write_text(p.stdout+p.stderr)
(T/'safety-backup-name.json').write_text(json.dumps({'file':str(T/'backups'/new[0])}));os.chmod(T/'safety-backup-name.json',0o600)
