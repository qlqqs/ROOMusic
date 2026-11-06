const {expect,tmp,base,admin,priv,evidence,api,client,save,scan,failure}=require('./browser-common.cjs');
(async()=>{const c=await client();try{
 const status=await(await api(c,'GET','/api/v1/setup/status')).json();
 if(status.setup_required)await api(c,'POST','/api/v1/setup/admin',admin,201);
 await api(c,'POST','/api/v1/auth/login',admin);await api(c,'GET','/api/v1/auth/me');await save(c);
 await api(c,'POST','/api/v1/library-roots',{path:'/music'},201,{'Idempotency-Key':require('crypto').randomUUID()});
 await scan(c,'b2');
 const groups=await(await api(c,'GET','/api/v1/release-groups?page_size=100')).json();
 const releases=await(await api(c,'GET','/api/v1/releases?page_size=100')).json();
 priv('groups.json',groups);priv('releases.json',releases);
 const selected=groups.items.slice(0,2).map(x=>x.id);for(const id of selected)await api(c,'PUT','/api/v1/me/favorites/albums/'+id);
 let tids=[];const best={};
 for(const item of groups.items){const rel=await(await api(c,'GET','/api/v1/releases/'+item.representative.id)).json();
  const tracks=rel.media.flatMap(x=>x.tracks);for(const [index,t] of tracks.entries()){
    if(tids.length<3&&t.source_kind!=='cue_virtual')tids.push(t.id);
    if(t.codec==='alac'&&t.source_kind!=='cue_virtual'&&t.cue===null&&(t.duration_seconds??0)>10&&(!best.alac||best.alac.duration>t.duration_seconds))best.alac={groupId:item.id,releaseId:item.representative.id,trackId:t.id,index,duration:t.duration_seconds};
  }
 }
 const pl=await(await api(c,'POST','/api/v1/me/playlists',{name:'恢复演练歌单',track_ids:tids},201,{'Idempotency-Key':require('crypto').randomUUID()})).json();
 // 只在临时凭据目录保存对象身份与完整私有回执，不输出真实曲名。
 priv('personal.json',{favorites:selected,playlist:pl,trackIds:tids});priv('playback-selection.json',best);
 priv('favorites-before.json',await(await api(c,'GET','/api/v1/me/favorites/albums')).json());
 const pls=await(await api(c,'GET','/api/v1/me/playlists')).json();priv('playlists-before.json',pls);await save(c);
 evidence('b2-result.json',{admin_created:true,auth_me_status:200,root:'/music',releases:releases.pagination.total,groups:groups.pagination.total,album_favorites:2,playlists:pls.items.length,playlist_tracks:tids.length,alac_selected:!!best.alac});
}finally{await c.dispose();}})().catch(failure);
