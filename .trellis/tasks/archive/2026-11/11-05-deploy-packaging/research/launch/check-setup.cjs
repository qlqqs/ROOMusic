// 只读确认正式实例的初始化页面；不点击按钮、不输入凭据、不发送写请求。
const fs = require('node:fs');
const path = require('node:path');
const { chromium, expect } = require('/home/qlqq/workspace/ROOMusic/frontend/node_modules/@playwright/test');
(async () => {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.toLowerCase().endsWith('_proxy')));
  const browser = await chromium.launch({ headless: true, env, args: ['--no-proxy-server'] });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const blockedWrites = [];
    await context.route('**/*', async route => {
      if (!['GET', 'HEAD'].includes(route.request().method())) {
        blockedWrites.push({method: route.request().method(), path: new URL(route.request().url()).pathname});
        await route.abort();
      } else await route.continue();
    });
    const page = await context.newPage();
    const response = await page.goto('http://192.168.50.206:8090', { waitUntil: 'networkidle', timeout: 30000 });
    expect(response.status()).toBe(200);
    await expect(page.getByRole('heading', { name: '创建管理员', exact: true })).toBeVisible();
    const setup = await page.request.get('http://192.168.50.206:8090/api/v1/setup/status');
    expect(setup.status()).toBe(200);
    expect((await setup.json()).setup_required).toBe(true);
    expect(blockedWrites).toEqual([]);
    await page.screenshot({ path: path.join(__dirname, 'setup-uninitialized.png'), fullPage: true, mask: [page.locator('input[type=password]')] });
    const result = { status: 200, setup_required: true, heading: '创建管理员', writes_sent: 0, blocked_writes: blockedWrites, screenshot: 'launch/setup-uninitialized.png' };
    fs.writeFileSync(path.join(__dirname, 'setup-result.json'), JSON.stringify(result, null, 2) + '\n');
    console.log(JSON.stringify(result));
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(`只读页面检查失败：${error.name}；不输出 DOM、请求体或敏感数据。`);
  process.exitCode = 1;
});
