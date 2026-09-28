import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { chromium } from '/home/qlqq/.npm/_npx/e41f203b7505f1fb/node_modules/playwright/index.mjs';

const base = process.env.ROOMUSIC_URL ?? 'http://127.0.0.1:5174';
const output = '/tmp/roomusic-verification';
await mkdir(output, { recursive: true });
const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [];
const api = [];
page.on('pageerror', (error) => errors.push(error.message));
page.on('request', (request) => { if (request.url().includes('/api/')) api.push(request.url()); });
const report = { checks: [], screenshots: [], accessibility: [] };
const button = (name) => page.getByRole('button', { name, exact: true });
const check = (name) => { report.checks.push(name); console.log(name); };
async function closeDialog() { await page.keyboard.press('Escape'); }
async function capture(name) {
  const path = `${output}/${name}.png`;
  await page.screenshot({ path, fullPage: true });
  report.screenshots.push(path);
}
async function checkGeometry() {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, '页面横向溢出');
  assert.equal(await page.locator('img').evaluateAll((images) => images.every((image) => image.complete && image.naturalWidth > 0)), true, '封面未加载');
}
async function accessibility(name) {
  await page.addScriptTag({ path: '/tmp/roomusic-browser-tools/node_modules/axe-core/axe.min.js' });
  const violations = await page.evaluate(async () => (await window.axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations.map(({ id, impact, nodes }) => ({ id, impact, targets: nodes.map((node) => node.target) })));
  report.accessibility.push({ name, violations });
}
try {
  await page.goto(`${base}/?demo=1`);
  await button('播放专辑').waitFor();
  await page.locator('img').first().waitFor();
  await capture('desktop-home');
  await checkGeometry();
  assert.equal(api.length, 0);
  check('演示首页无 API 请求且封面可用');
  await button('播放专辑').click();
  await button('暂停').waitFor();
  await page.waitForTimeout(1200);
  assert.ok(Number(await page.getByRole('slider', { name: '播放进度' }).inputValue()) >= 1);
  await button('暂停').click();
  const paused = await page.getByRole('slider', { name: '播放进度' }).inputValue();
  await page.waitForTimeout(1100);
  assert.equal(await page.getByRole('slider', { name: '播放进度' }).inputValue(), paused);
  await page.getByRole('slider', { name: '播放进度' }).fill('90');
  await page.getByRole('slider', { name: '音量' }).fill('31');
  await button('静音').click();
  await button('取消静音').click();
  await button('顺序播放').click();
  await button('列表循环').click();
  await button('单曲循环').click();
  await button('随机播放').click();
  await button('随机播放').click();
  check('播放计时、暂停、进度、音量及模式可操作');
  await page.getByRole('button', { name: '喜欢 向山而行', exact: true }).first().click();
  await page.getByRole('button', { name: '向山而行 更多操作', exact: true }).first().click();
  await page.getByRole('dialog').getByLabel('歌单名称').fill('夜间散步');
  await page.getByRole('dialog').getByRole('button', { name: '创建歌单' }).click();
  await page.getByRole('link', { name: /我的音乐/ }).click();
  await page.getByRole('heading', { name: '我的音乐', exact: true }).waitFor();
  assert.equal(await page.locator('.music-song-list > li').count(), 1);
  await page.getByRole('button', { name: /^歌单/ }).click();
  await page.getByRole('button', { name: /夜间散步.*首歌曲/ }).click();
  await page.getByRole('heading', { name: '夜间散步', exact: true }).waitFor();
  assert.equal(await page.locator('.music-song-list > li').count(), 1);
  await button('重命名歌单').click();
  await page.getByRole('dialog').getByLabel('歌单名称').fill('雨后散步');
  await button('保存名称').click();
  await button('播放全部').click();
  await page.reload();
  await button('播放').waitFor();
  assert.equal(await page.locator('.music-song-list > li').count(), 1);
  assert.equal(await page.getByRole('slider', { name: '音量' }).inputValue(), '31');
  await capture('desktop-playlist');
  check('收藏、歌单创建重命名、播放与刷新暂停恢复');
  await button('歌词').click();
  await page.getByText('暂无歌词', { exact: true }).waitFor();
  await closeDialog();
  assert.equal(await button('歌词').evaluate((element) => element === document.activeElement), true);
  await page.getByRole('button', { name: /^播放队列（/ }).click();
  await button('清空队列').click();
  await page.getByText('队列为空', { exact: true }).waitFor();
  await closeDialog();
  assert.equal(await button('播放').isDisabled(), true);
  check('歌词空态、Esc 焦点恢复、清空队列停止播放');
  await button('删除歌单').click();
  await button('确认删除').click();
  assert.equal(await page.locator('.playlist-strip > button').count(), 0);
  check('歌单确认删除');
  await page.getByRole('link', { name: /专辑库/, exact: false }).first().click();
  await page.getByRole('textbox', { name: '搜索演示专辑' }).fill('爵士');
  await page.waitForTimeout(150);
  assert.equal(await page.locator('.demo-album').count(), 1);
  await page.reload();
  assert.equal(await page.getByRole('textbox', { name: '搜索演示专辑' }).inputValue(), '爵士');
  check('专辑搜索及 URL 恢复');
  await page.goto(`${base}/?demo=1#/`);
  await button('播放专辑').click();
  await button('暂停').click();
  await page.getByRole('button', { name: '风经过松林 更多操作', exact: true }).click();
  await button('下一首播放').click();
  await page.getByRole('button', { name: /^播放队列（5）/ }).click();
  assert.equal(await page.locator('.queue-list > li').nth(1).locator('strong').innerText(), '风经过松林');
  await page.locator('.queue-list > li').nth(1).getByRole('button', { name: '下移 风经过松林', exact: true }).click();
  await page.locator('.queue-list > li').nth(2).getByRole('button', { name: '移除 风经过松林', exact: true }).click();
  assert.equal(await page.locator('.queue-list > li').count(), 4);
  await closeDialog();
  check('下一首插入、重复曲目独立移动和移除');
  await accessibility('desktop-home');
  for (const width of [900, 375]) {
    await page.setViewportSize({ width, height: 900 });
    await checkGeometry();
    await capture(`${width}-home`);
    await page.getByRole('button', { name: /^播放队列（/ }).click();
    await capture(`${width}-queue`);
    await checkGeometry();
    if (width === 375) await accessibility('mobile-queue');
    await closeDialog();
    await page.getByRole('link', { name: /我的音乐/ }).click();
    await capture(`${width}-music`);
    await checkGeometry();
    await page.getByRole('link', { name: /首页/ }).click();
    if (width === 375) {
      await button('音量设置').click();
      await page.getByRole('dialog').getByRole('slider', { name: '音量' }).fill('48');
      await page.getByRole('checkbox', { name: '静音', exact: true }).check();
      await closeDialog();
      const overlap = await page.evaluate(() => {
        const label = document.querySelector('.simulation-label').getBoundingClientRect();
        return [...document.querySelectorAll('.transport-buttons button')].some((element) => {
          const rect = element.getBoundingClientRect();
          return label.left < rect.right && label.right > rect.left && label.top < rect.bottom && label.bottom > rect.top;
        });
      });
      assert.equal(overlap, false, '模拟状态标签遮挡播放控件');
      check('移动端音量和静音可操作，状态标签不遮挡播放控件');
    }
  }
  assert.equal(api.length, 0);
  assert.deepEqual(errors, []);
  check('桌面和移动端无横向溢出，无运行错误，演示全程无 API 请求');
} finally {
  await writeFile(`${output}/report.json`, JSON.stringify({ ...report, errors, api }, null, 2));
  await browser.close();
}
