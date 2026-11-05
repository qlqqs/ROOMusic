const crypto=require('node:crypto');
const {execFileSync}=require('node:child_process');
const {fs,path,expect,http,https,admin,tmp,save,launch,capture,login,privateWrite,safeFailure}=require('./browser-common.cjs');
(async()=>{
const start=new Date().toISOString();
const direct=await launch();const tls=await launch();
try{
 const wrong=crypto.randomBytes(18).toString('hex');
 console.log('步骤 8.1：HTTP 同一用户名连续五次错误密码，第六次应限速。');
 const attempts=[];
 for(let i=1;i<=6;i++){
   const response=await login(direct.page,http,wrong);
   const body=await response.json();
   const item={attempt:i,status:response.status(),code:body.error?.code,retry_after:response.headers()['retry-after']??null,request_id:response.headers()['x-request-id']};
   attempts.push(item);console.log(JSON.stringify(item));
   expect(response.status()).toBe(i<=5?401:429);
   expect(body.error?.code).toBe(i<=5?'invalid_credentials':'too_many_attempts');
   if(i===6)expect(Number(item.retry_after)).toBeGreaterThan(0);
 }
 await expect(direct.page.getByText('登录失败次数太多，请过几分钟再试。',{exact:true})).toBeVisible();
 await capture(direct.page,'step-08-http-throttled.png','auth');
 console.log('步骤 8.2：HTTPS 先故意输错一次取得 TCP 来源证据，再用同一用户名正确登录。');
 const probe=await login(tls.page,https,wrong);expect(probe.status()).toBe(401);
 console.log(JSON.stringify({origin:https,probe_status:probe.status(),request_id:probe.headers()['x-request-id']}));
 const good=await login(tls.page,https);expect(good.status()).toBe(200);
 await expect.poll(async()=>(await tls.page.request.get(https+'/api/v1/auth/me')).status()).toBe(200);
 const cookie=(await tls.context.cookies()).find(x=>x.name==='roomusic_session');expect(cookie?.secure).toBe(true);
 await tls.page.goto(https+'/library');
 await expect(tls.page.getByLabel('目录路径',{exact:true})).toBeVisible();
 console.log(JSON.stringify({origin:https,correct_login_status:good.status(),secure:cookie.secure,request_id:good.headers()['x-request-id']}));
 await capture(tls.page,'step-08-https-logged-in.png','scan');
 await save(tls.context,'https-state.json');
 // TLS 成功后 HTTP 的账号+来源锁仍应存在：证明成功只清除本组合。
 const still=await login(direct.page,http);expect(still.status()).toBe(429);
 console.log(JSON.stringify({origin:http,correct_login_during_same_lock:still.status(),retry_after:still.headers()['retry-after']}));
 const raw=execFileSync('docker',['logs','--since',start,'roomusic-drill-app-1'],{encoding:'utf8',stdio:['ignore','pipe','pipe']});
 const records=raw.split('\n').flatMap(line=>{try{return [JSON.parse(line)];}catch{return [];}}).filter(x=>['identity.login.failed','identity.login.throttled'].includes(x.event)).map(x=>Object.fromEntries(['time','event','source','username','consecutive_failures','scope','retry_after_seconds','request_id'].filter(k=>x[k]!==undefined).map(k=>[k,x[k]])));
 for(const event of records)console.log(JSON.stringify(event));
 const sourceHttp=records.find(x=>x.event==='identity.login.throttled'&&x.username===admin.username)?.source;
 const sourceHttps=records.find(x=>x.request_id===probe.headers()['x-request-id']&&x.event==='identity.login.failed')?.source;
 expect(sourceHttp).toBe('192.168.50.206');expect(sourceHttps).toBe('10.203.51.1');
 const pause={completed_at:new Date().toISOString(),step:'8.3',username:admin.username,source_http:sourceHttp,source_https:sourceHttps,https_authenticated_state:'https-state.json'};
 privateWrite('pause-8.3.json',pause);
 console.log(JSON.stringify(pause));
 console.log('结果：8.1–8.2 通过；到达 8.3 强制暂停。保留演练实例，不执行第 9 步。');
}finally{await direct.browser.close();await tls.browser.close();}
})().catch(safeFailure);
