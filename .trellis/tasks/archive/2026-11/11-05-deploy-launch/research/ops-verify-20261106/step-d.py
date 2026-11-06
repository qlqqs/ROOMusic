from drilldb import *
import shutil
# 在清理前，仅归档无凭据、无真实标题/路径/图像的封面校验明细。
before=json.loads((T/'artwork-http-before.json').read_text());lost=json.loads((T/'artwork-http-lost.json').read_text());after=json.loads((T/'artwork-http-after.json').read_text())
details=[]
for i,a in enumerate(before):
    b=next(x for x in lost if x['release_id']==a['release_id']);c=next(x for x in after if x['release_id']==a['release_id'])
    details.append({'sample':i+1,'source':a['source_type'],'cue':a['has_cue'],'multi_disc':a['medium_count']>1,'http_before':a['status'],'http_lost':b['status'],'http_after':c['status'],'bytes_before':a['bytes'],'bytes_after':c['bytes'],'sha256_before':a['sha256'],'sha256_after':c['sha256']})
save('c-artwork-detailed-evidence.json',details)
# 记录演练资源归属，包括镜像自带 VOLUME 所产生的匿名卷。
ids=out(['docker','ps','-aq','--filter','label=com.docker.compose.project=roomusic-drill']).splitlines()
owned_vols={};containers=[]
for cid in ids:
    project=out(['docker','inspect','-f','{{index .Config.Labels "com.docker.compose.project"}}',cid]);assert project=='roomusic-drill'
    name=out(['docker','inspect','-f','{{.Name}}',cid]);assert name.startswith('/roomusic-drill-')
    mounts=json.loads(out(['docker','inspect','-f','{{json .Mounts}}',cid]))
    for m in mounts:
        if m['Type']=='volume':
            assert m['Name'] not in json.loads((E/'prod-all-volume-creation-before.json').read_text())
            assert m['Name']!='roomusic_postgres-data'
            owned_vols[m['Name']]=out(['docker','volume','inspect','-f','{{.CreatedAt}}',m['Name']])
    containers.append({'id':cid,'name':name,'mounts':mounts})
save('d-drill-cleanup-precheck.json',{'containers':containers,'volumes':owned_vols,'network':out(['docker','network','inspect','-f','{{index .Labels "com.docker.compose.project"}}','roomusic-drill_roomusic'])})
p=run(D+['down','--volumes','--remove-orphans']);(E/'d-compose-down.log').write_text(p.stdout+p.stderr)
assert not out(['docker','ps','-aq','--filter','label=com.docker.compose.project=roomusic-drill'])
assert not out(['docker','network','ls','-q','--filter','label=com.docker.compose.project=roomusic-drill'])
assert not out(['docker','volume','ls','-q','--filter','label=com.docker.compose.project=roomusic-drill'])
for v in owned_vols:assert run(['docker','volume','inspect','-f','{{.Name}}',v],check=False).returncode!=0
# 删除的路径硬编码且不接受外部参数；不复制 env、会话、管理员凭据或数据库 dump。
assert str(T)=='/tmp/roomusic-drill' and T.is_dir() and not T.is_symlink()
with (E/'commands.log').open('a') as f:f.write('Python shutil.rmtree("/tmp/roomusic-drill")（仅本次演练临时目录，凭据不归档）\n')
shutil.rmtree(T)
# 最后一次只读核对正式与开发实例。
last=baseline();save('baseline-after.json',last)
first=json.loads((E/'baseline-before.json').read_text())
vc=json.loads((E/'prod-all-volume-creation-before.json').read_text())
lastvc={n:out(['docker','volume','inspect','-f','{{.CreatedAt}}',n]) for n in vc};save('prod-all-volume-creation-after.json',lastvc)
images=out(['docker','image','ls','--no-trunc','--format','{{.Repository}}:{{.Tag}} {{.ID}}','--filter','reference=roomusic-prod-*'])
(E/'prod-images-after.txt').write_text(images+'\n')
result={'drill_containers_network_volumes_removed':True,'drill_anonymous_volumes_removed':True,'temporary_credentials_removed':not T.exists(),'development_baseline_equal':first['dev']==last['dev'],'prod_three_volume_creation_equal':vc==lastvc,'prod_backup_inventory_metadata_sha256_equal':first['prod_backup_files']==last['prod_backup_files'],'prod_all_healthy':all(v['health']=='healthy' and v['status']=='running' for v in last['prod_state'].values()),'prod_setup_required':last['prod_endpoints']['/api/v1/setup/status']['body']['setup_required'],'prod_images_unchanged':images+'\n'==(E/'prod-images-before.txt').read_text(),'prod_mounts_equal':first['prod_mounts']==last['prod_mounts'],'git_head_unchanged':first['git_head']==last['git_head'],'git_clean':last['git_status']=='','new_image_tags_created':False}
import socket
s=socket.socket();result['drill_port_18090_free']=s.connect_ex(('127.0.0.1',18090))!=0;s.close()
save('d-result.json',result)
assert all(v for k,v in result.items() if k!='new_image_tags_created')
