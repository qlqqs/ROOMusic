const {fs,tmp,base,launch,expect,evidence,failure,command}=require('./browser-common.cjs');
(async()=>{const phase=process.argv[2],track=JSON.parse(fs.readFileSync(tmp+'/playback-selection.json')).alac;const {browser,context,page}=await launch();try{
await page.addInitScript(()=>{window.drillMedia=[];const old=HTMLMediaElement.prototype.play;HTMLMediaElement.prototype.play=function(){if(!window.drillMedia.includes(this))window.drillMedia.push(this);return old.call(this);};});
const errors=[];page.on('pageerror',()=>errors.push('pageerror'));
const preflights=[],audio=[];page.on('response',async r=>{if(r.url().endsWith('/tracks/'+track.trackId+'/playback')){try{const d=await r.json();preflights.push({status:r.status(),kind:d.representation?.kind??d.status});}catch{}}if(r.url().endsWith('/tracks/'+track.trackId+'/audio'))audio.push({status:r.status(),type:r.headers()['content-type']});});
command('Chromium：打开同一 ALAC 专辑页，点击真实播放按钮，观察原生媒体时间后暂停（'+phase+'）');
await page.goto(base+'/albums/'+track.groupId+'?release='+track.releaseId);await page.locator('ol > li').nth(track.index).getByRole('button',{name:'从这首开始播放',exact:true}).click();
const media=()=>page.evaluate(()=>{const a=window.drillMedia.at(-1);return a?{time:a.currentTime,paused:a.paused,error:a.error?.code??null}:null;});
await expect.poll(async()=>(await media())?.paused,{timeout:120000}).toBe(false);
const before=await media(),wall=Date.now();await expect.poll(async()=>(await media())?.time??0,{timeout:20000}).toBeGreaterThan(before.time+2);const after=await media();expect(after.error).toBeNull();expect(after.time-before.time).toBeLessThan((Date.now()-wall)/1000+1);
await expect.poll(()=>preflights.some(x=>x.status===200&&x.kind==='derived')).toBe(true);expect(audio.some(x=>[200,206].includes(x.status)&&x.type==='audio/flac')).toBe(true);
await page.getByRole('region',{name:'播放器',exact:true}).getByRole('button',{name:'暂停',exact:true}).click();
evidence('c-'+phase+'-alac.json',{phase,same_track:true,codec:'alac',before_seconds:before.time,after_seconds:after.time,wall_seconds:(Date.now()-wall)/1000,error:after.error,preflights,audio,page_errors:errors.length,paused_after:true});
}finally{await browser.close();}})().catch(failure);
