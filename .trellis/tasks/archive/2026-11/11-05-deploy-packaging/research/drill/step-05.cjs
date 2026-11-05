const {fs,path,expect,http,tmp,save,launch,json,capture,privateWrite,safeFailure}=require('./browser-common.cjs');
(async()=>{
const {browser,context,page}=await launch('http-state.json');
try{
  console.log('步骤 5：读取演练库 API，在内存里选择原始 FLAC 与 ALAC；不输出文件名/标题。');
  const selection=JSON.parse(fs.readFileSync(path.join(tmp,'catalog-selection.json'),'utf8'));
  const best={};
  for(const entry of selection.representatives){
    const release=await json(await page.request.get(http+'/api/v1/releases/'+entry.releaseId),'发行详情');
    const tracks=release.media.flatMap(m=>m.tracks);
    tracks.forEach((track,index)=>{
      if(['flac','alac'].includes(track.codec)&&track.source_kind!=='cue_virtual'&&track.cue===null&&(track.duration_seconds??0)>10){
        if(!best[track.codec]||track.duration_seconds<best[track.codec].duration){
          best[track.codec]={...entry,trackId:track.id,index,duration:track.duration_seconds,sourceKind:track.source_kind};
        }
      }
    });
  }
  expect(Object.keys(best).sort()).toEqual(['alac','flac']);
  privateWrite('playback-selection.json',best);
  // 只观测原生媒体，原样调用原生 play；不伪造解码、时间、网络或事件。
  await page.addInitScript(()=>{
    window.drillMedia=[];
    const play=HTMLMediaElement.prototype.play;
    HTMLMediaElement.prototype.play=function(){if(!window.drillMedia.includes(this))window.drillMedia.push(this);return play.call(this);};
  });
  const media=()=>page.evaluate(()=>{const a=window.drillMedia.at(-1);return a?{time:a.currentTime,paused:a.paused,readyState:a.readyState,error:a.error?.code??null}:null;});
  for(const codec of ['flac','alac']){
    const track=best[codec];const preflights=[];const audio=[];
    const listener=async response=>{
      if(response.url().endsWith('/tracks/'+track.trackId+'/playback')){
        try{const data=await response.json();preflights.push({status:response.status(),kind:data.representation?.kind??data.status});}catch{}
      }
      if(response.url().endsWith('/tracks/'+track.trackId+'/audio'))audio.push({status:response.status(),type:response.headers()['content-type']});
    };
    page.on('response',listener);
    console.log(`点击 ${codec.toUpperCase()} 对应曲目行的真实播放按钮。`);
    await page.goto(http+'/albums/'+track.groupId+'?release='+track.releaseId);
    const row=page.locator('ol > li').nth(track.index);
    await row.getByRole('button',{name:'从这首开始播放',exact:true}).click();
    await expect.poll(async()=>(await media())?.paused,{timeout:120000}).toBe(false);
    const before=await media();const wall=Date.now();
    await expect.poll(async()=>(await media())?.time??0,{timeout:15000}).toBeGreaterThan(before.time+2);
    const after=await media();
    expect(after.error).toBeNull();
    expect(after.time-before.time).toBeLessThan((Date.now()-wall)/1000+1);
    await expect.poll(()=>preflights.some(x=>x.status===200&&x.kind===(codec==='alac'?'derived':'original'))).toBe(true);
    expect(audio.some(x=>[200,206].includes(x.status)&&x.type==='audio/flac')).toBe(true);
    await page.getByRole('link',{name:'正在播放',exact:true}).click();
    if(codec==='alac'){
      const server=page.getByRole('heading',{name:'服务端',exact:true}).locator('..');
      await expect(server).toContainText('无损转换');
    }
    console.log(JSON.stringify({codec,before:before.time,after:after.time,advance_seconds:after.time-before.time,elapsed_seconds:(Date.now()-wall)/1000,paused:after.paused,error:after.error,preflights,audio}));
    await capture(page,`step-05-${codec}.png`);
    await page.getByRole('region',{name:'播放器',exact:true}).getByRole('button',{name:'暂停',exact:true}).click();
    page.off('response',listener);
  }
  await save(context);
  console.log('结果：FLAC 直出与 ALAC 派生均由真实 Chromium 解码，真实时间推进，截图已遮挡真实曲目/封面。');
}finally{await browser.close();}
})().catch(safeFailure);
