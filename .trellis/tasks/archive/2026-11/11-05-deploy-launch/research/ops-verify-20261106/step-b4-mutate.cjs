const {fs,tmp,expect,priv,evidence,api,client,failure}=require('./browser-common.cjs');
(async()=>{const c=await client();try{
const p=JSON.parse(fs.readFileSync(tmp+'/personal.json'));const playlist=p.playlist.playlist;
await api(c,'DELETE','/api/v1/me/playlists/'+playlist.id,{expected_revision:playlist.revision},200,{'Idempotency-Key':require('crypto').randomUUID()});
await api(c,'DELETE','/api/v1/me/favorites/albums/'+p.favorites[0]);
const pls=await(await api(c,'GET','/api/v1/me/playlists')).json();const fav=await(await api(c,'GET','/api/v1/me/favorites/albums')).json();
expect(pls.items).toHaveLength(0);expect(fav.items).toHaveLength(1);
evidence('b4-mutated-api.json',{playlists:pls.items.length,favorite_albums:fav.items.length});
}finally{await c.dispose();}})().catch(failure);
