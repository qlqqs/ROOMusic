const {fs,path,expect,http,https,tmp,save,launch,json,capture,login,safeFailure}=require('./browser-common.cjs');
(async()=>{
const groups=JSON.parse(fs.readFileSync(path.join(tmp,'catalog-selection.json'),'utf8')).groupIds;
for(const [index,origin] of [http,https].entries()){
const {browser,context,page}=await launch();
try{
  console.log(`步骤 7：${origin} 从无会话浏览器登录。`);
  const response=await login(page,origin);expect(response.status()).toBe(200);
  await expect.poll(async()=>(await page.request.get(origin+'/api/v1/auth/me')).status()).toBe(200);
  const cookie=(await context.cookies()).find(x=>x.name==='roomusic_session');
  expect(cookie).toBeDefined();expect(cookie.secure).toBe(origin===https);expect(cookie.httpOnly).toBe(true);expect(cookie.sameSite).toBe('Lax');expect(cookie.path).toBe('/');
  console.log(JSON.stringify({origin,login_status:response.status(),cookie:{name:cookie.name,secure:cookie.secure,httpOnly:cookie.httpOnly,sameSite:cookie.sameSite,path:cookie.path}}));
  await page.goto(origin+'/albums/'+groups[index]);
  const favorite=page.getByRole('button',{name:/^收藏《/}).first();
  await expect(favorite).toBeEnabled();await expect(favorite).toHaveAttribute('aria-pressed','false');
  const pending=page.waitForResponse(r=>r.url().endsWith('/api/v1/me/favorites/albums/'+groups[index])&&r.request().method()==='PUT');
  await favorite.click();const write=await pending;expect(write.status()).toBe(200);
  await expect(favorite).toHaveAttribute('aria-pressed','true');
  const result=await write.json();expect(result.favorited).toBe(true);
  console.log(JSON.stringify({origin,favorite_status:write.status(),favorited:result.favorited,pressed:await favorite.getAttribute('aria-pressed')}));
  await favorite.screenshot({path:path.join(__dirname,`step-07-${index===0?'http':'https'}-favorite.png`)});
  const out=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/logout')&&r.request().method()==='POST');
  await page.getByRole('button',{name:'退出登录',exact:true}).click();const logout=await out;expect(logout.status()).toBe(200);
  await expect(page.getByLabel('用户名',{exact:true})).toBeVisible();
  expect((await context.cookies()).some(x=>x.name==='roomusic_session')).toBe(false);
  console.log(JSON.stringify({origin,logout_status:logout.status(),cookie_removed:true}));
  await capture(page,`step-07-${index===0?'http':'https'}-logout.png`,'auth');
}finally{await browser.close();}
}
console.log('结果：HTTP/HTTPS 各登录、收藏不同专辑、退出均成功；Cookie Secure 跟随来源。');
})().catch(safeFailure);
