// 演练浏览器辅助：凭据/会话仅放在 /tmp，记录只包含聚合、状态码与脱敏截图。
const fs = require('node:fs');
const path = require('node:path');
const { chromium, expect } = require('/home/qlqq/workspace/ROOMusic/frontend/node_modules/@playwright/test');
const http = 'http://192.168.50.206:18090';
const https = 'https://localhost:18443';
const tmp = '/tmp/roomusic-drill';
const admin = JSON.parse(fs.readFileSync(path.join(tmp, 'admin.json'), 'utf8'));
function privateWrite(name, data) {
  const file = path.join(tmp, name);
  fs.writeFileSync(file, JSON.stringify(data), {mode: 0o600});
  fs.chmodSync(file, 0o600);
}
async function save(context, name = 'http-state.json') { privateWrite(name, await context.storageState()); }
async function launch(state) {
  // 本机继承代理会把局域网导航送到外部代理；仅浏览器子进程清除代理变量。
  const env = Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.toLowerCase().endsWith('_proxy')));
  const browser = await chromium.launch({headless:true, env, args:['--no-proxy-server']});
  const context = await browser.newContext({viewport:{width:1440,height:1000}, ignoreHTTPSErrors:true, ...(state ? {storageState:path.join(tmp,state)} : {})});
  context.setDefaultTimeout(20000);
  const page = await context.newPage();
  return {browser, context, page};
}
async function json(response, label) {
  if (!response.ok()) throw new Error(`${label}: HTTP ${response.status()}`);
  return response.json();
}
async function capture(page, name, mode='private') {
  // 浏览器原生遮罩在截图编码前应用；不先保存含原始真实音乐信息的图片。
  const masks=[page.locator('img'),page.locator('input[type=password]')];
  if (mode==='private') masks.push(page.locator('#main'), page.locator('[aria-label="播放器"] > div').first());
  if (mode==='scan') masks.push(page.locator('ul'), page.locator('[class*="diagnostic"]'), page.locator('[aria-label="播放器"] > div').first());
  await page.screenshot({path:path.join(__dirname,name),fullPage:true,mask:masks,maskColor:'#333333'});
}
async function login(page, origin, password=admin.password) {
  await page.goto(origin+'/login');
  await page.getByLabel('用户名',{exact:true}).fill(admin.username);
  await page.getByLabel('密码',{exact:true}).fill(password);
  const pending=page.waitForResponse(r=>r.url().endsWith('/api/v1/auth/login')&&r.request().method()==='POST');
  await page.getByRole('button',{name:'登录',exact:true}).click();
  return pending;
}
function safeFailure(error) {
  // 错误详情可能含 DOM 中的真实标题/文件名；只记录类型，定位细节由检查脚本明确阶段给出。
  console.error(`检查中止：${error.name || 'Error'}；详细 DOM/请求内容不输出，避免泄露凭据或真实媒体元数据。`);
  process.exitCode=1;
}
module.exports={fs,path,expect,http,https,tmp,admin,privateWrite,save,launch,json,capture,login,safeFailure};
