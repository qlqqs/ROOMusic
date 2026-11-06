from drilldb import *
c=counts();save('b2-db-counts.json',c)
p=run(D+['exec','-T','backup','/bin/sh','/opt/roomusic/backup.sh','now'])
(E/'b2-manual-backup.log').write_text(p.stdout+p.stderr)
name=json.loads(p.stdout.strip())['file'];assert pathlib.Path(name).name==name
(T/'chosen-backup.json').write_text(json.dumps({'file':str(T/'backups'/name)}));os.chmod(T/'chosen-backup.json',0o600)
assert (T/'backups'/name).is_file()
env=dict(os.environ,COMPOSE_PROJECT_NAME='roomusic-drill',COMPOSE_ENV_FILES=str(T/'drill.env'))
# 环境仅记录非凭据恢复选择器。
with (E/'commands.log').open('a') as f:f.write('恢复选择器：COMPOSE_PROJECT_NAME=roomusic-drill COMPOSE_ENV_FILES=/tmp/roomusic-drill/drill.env\n')
t=time.monotonic();p=run(['deploy/production/restore.sh','check',str(T/'backups'/name)],env=env)
(E/'b3-restore-check.log').write_text(p.stdout+p.stderr)
rows=[l.split('\t') for l in p.stdout.strip().splitlines()[1:]]
matched=all(a==b for _,a,b in rows)
save('b3-result.json',{'rc':p.returncode,'seconds':round(time.monotonic()-t,3),'backup':name,'size':(T/'backups'/name).stat().st_size,'rows_all_equal':matched,'comparison':p.stdout,'personal_counts':{k:c[k] for k in ['favorite_albums','personal_playlists','personal_playlist_items']}})
assert matched
