const {fs,tmp,expect,priv,evidence,api,client,save,failure,admin}=require('./browser-common.cjs');
(async()=>{const c=await client('no-existing-session.json');try{
await api(c,'POST','/api/v1/auth/login',admin);await api(c,'GET','/api/v1/auth/me');await save(c);
const p=JSON.parse(fs.readFileSync(tmp+'/personal.json'));const id=p.playlist.playlist.id;
const pl=await(await api(c,'GET','/api/v1/me/playlists/'+id)).json();
const items=await(await api(c,'GET','/api/v1/me/playlists/'+id+'/items')).json();
expect(pl.playlist.id).toBe(id);expect(pl.playlist.name).toBe(p.playlist.playlist.name);expect(pl.playlist.track_count).toBe(3);expect(items.items.map(x=>x.track.track_id)).toEqual(p.trackIds);
const fav=await(await api(c,'POST','/api/v1/me/favorites/lookup',{group_ids:p.favorites})).json();
expect(fav.albums).toHaveLength(2);expect(fav.albums.every(x=>x.favorited)).toBe(true);
evidence('b4-login-api-result.json',{fresh_original_password_login:200,auth_me:200,original_playlist_id_restored:true,original_playlist_name_restored:true,original_track_order_restored:true,playlist_tracks:pl.playlist.track_count,restored_favorite_albums:fav.albums.filter(x=>x.favorited).length});
}finally{await c.dispose();}})().catch(failure);
