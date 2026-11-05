// 按用户要求由本机尝试 A；不能作为另一台局域网设备的来源地址验收。
const crypto = require('node:crypto');
const {expect,http,launch,login,capture,safeFailure}=require('./browser-common.cjs');
(async()=>{
  const {browser,page}=await launch();
  try{
    console.log('A 的本机替代测试：宿主机浏览器直接访问 HTTP 局域网入口（不是另一台设备）。');
    const response=await login(page,http,crypto.randomBytes(18).toString('hex'));
    const body=await response.json();
    expect(response.status()).toBe(401);
    expect(body.error?.code).toBe('invalid_credentials');
    await expect(page.getByText('用户名或密码错误。',{exact:true})).toBeVisible();
    console.log(JSON.stringify({origin:http,status:response.status(),code:body.error.code,request_id:response.headers()['x-request-id'],coverage:'host_only_not_another_lan_device'}));
    await capture(page,'step-08-local-a.png','auth');
  }finally{await browser.close();}
})().catch(safeFailure);
