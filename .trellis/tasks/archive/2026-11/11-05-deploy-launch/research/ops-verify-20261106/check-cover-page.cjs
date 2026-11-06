const {fs,tmp,base,launch,evidence,failure,command}=require('./browser-common.cjs');
(async()=>{const phase=process.argv[2];const arts=JSON.parse(fs.readFileSync(tmp+'/artwork-before.json'));const groups=JSON.parse(fs.readFileSync(tmp+'/groups.json'));const chosen=[];for(const [label,f] of [['embedded',x=>x.source_type==='embedded'],['folder',x=>x.source_type==='folder'],['multi_disc',x=>x.medium_count>1]]){const a=arts.find(f);if(a)chosen.push({label,art:a});}
const {browser,context,page}=await launch();try{const results=[];let errors=0;page.on('pageerror',()=>errors++);
for(const {label,art} of chosen){const g=groups.items.find(x=>x.representative.id===art.release_id);if(!g){results.push({category:label,not_found_in_selection:true});continue;}
command('Chromium：观察 '+label+' 专辑详情页封面状态与页面异常计数（'+phase+'；不保存音乐内容）');
const r=await page.goto(base+'/albums/'+g.id+'?release='+art.release_id);await page.locator('[data-variant="detail"] [data-state]').waitFor({state:'visible'});await page.waitForFunction(()=>{const e=document.querySelector('[data-variant="detail"] [data-state]');return e&&e.dataset.state!=='loading';},{},{timeout:20000});
results.push({category:label,page_status:r.status(),cover_state:await page.locator('[data-variant="detail"] [data-state]').getAttribute('data-state'),failure_caption_visible:await page.getByText('封面无法加载',{exact:true}).isVisible(),retry_visible:await page.getByRole('button',{name:'重新加载封面',exact:true}).isVisible(),track_rows:await page.locator('ol > li').count()});}
evidence('c-'+phase+'-pages.json',{phase,page_errors:errors,pages:results});
}finally{await browser.close();}})().catch(failure);
