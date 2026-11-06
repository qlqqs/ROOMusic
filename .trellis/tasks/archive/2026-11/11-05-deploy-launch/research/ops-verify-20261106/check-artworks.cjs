const {fs,tmp,client,base,evidence,priv,failure,command}=require('./browser-common.cjs');const crypto=require('crypto');
(async()=>{const phase=process.argv[2];const original=JSON.parse(fs.readFileSync(tmp+'/artwork-before.json'));const current=phase==='after'?JSON.parse(fs.readFileSync(tmp+'/artwork-after.json')):original;const ctx=await client();try{
const checked=[];
for(const art of original){const route='/api/v1/artworks/'+art.storage_key;command('HTTP GET '+base+route+' [带Cookie，不记录值；无条件请求]');const r=await ctx.get(base+route,{headers:{'Cache-Control':'no-cache'}});let sha=null,bytes=0;if(r.status()===200){const b=await r.body();bytes=b.length;sha=crypto.createHash('sha256').update(b).digest('hex');}checked.push({release_id:art.release_id,storage_key:art.storage_key,status:r.status(),type:r.headers()['content-type'],bytes,sha256:sha,source_type:art.source_type,has_cue:art.has_cue,medium_count:art.medium_count});}
priv('artwork-http-'+phase+'.json',checked);
const categories={embedded:x=>x.source_type==='embedded',folder:x=>x.source_type==='folder',cue:x=>x.has_cue,multi_disc:x=>x.medium_count>1};
let summary={phase,checked:checked.length,status_counts:Object.fromEntries([...new Set(checked.map(x=>x.status))].map(s=>[s,checked.filter(x=>x.status===s).length])),categories:Object.fromEntries(Object.entries(categories).map(([k,f])=>{const a=checked.filter(f);return[k,{total:a.length,http200:a.filter(x=>x.status===200).length,http404:a.filter(x=>x.status===404).length}];}))};
if(phase==='after'){
const prior=JSON.parse(fs.readFileSync(tmp+'/artwork-http-before.json'));summary.bytes_and_hashes_fully_restored=prior.every(a=>checked.some(b=>b.release_id===a.release_id&&b.status===200&&b.sha256===a.sha256));summary.association_identity_unchanged=JSON.stringify(original)===JSON.stringify(current);summary.differences=prior.filter(a=>!checked.some(b=>b.release_id===a.release_id&&b.status===200&&b.sha256===a.sha256)).map((a,i)=>({sample:'差异-'+(i+1),source_type:a.source_type,has_cue:a.has_cue,multi_disc:a.medium_count>1,status:checked.find(b=>b.release_id===a.release_id)?.status??'missing'}));
}
evidence('c-'+phase+'-artwork-http.json',summary);
}finally{await ctx.dispose();}})().catch(failure);
