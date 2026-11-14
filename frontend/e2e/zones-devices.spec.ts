import type { Locator, Page } from "@playwright/test";
import { personalTrack, seedPersonalTracks } from "../src/test/personal-data";
import type { ApiMock } from "./support/api-mock";
import { expectNoHorizontalOverflow, expectNoSeriousViolations, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { MOCK_ZONE } from "./support/zones";

const BASE = "/api/v1/me/playback";
const TARGET_KEY = "roomusic:control-target:v1:00000000-0000-4000-8000-000000000003";
const bar = (page: Page) => page.getByRole("region", { name: "播放器", exact: true });
const picker = (page: Page) => page.getByRole("dialog", { name: "播放设备", exact: true });
// 设备按钮只有图标：控制本机时名为“播放设备”，控制别的设备时名为“正在控制：设备名”，所在的外层带 data-remote。
const deviceButton = (page: Page) => bar(page).getByRole("button", { name: /^(播放设备|正在控制：)/ });
const fullscreenDialog = (page: Page) => page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "收起全屏播放器", exact: true }) });
const actions = (page: Page, name: string) => picker(page).getByRole("group", { name: `${name}的操作`, exact: true });

async function setup(page: Page, mock: ApiMock, fixed = false) {
  const localTrack = personalTrack(701, { title: "本机合成曲目" });
  const remoteTrack = personalTrack(702, { title: "远端合成曲目" });
  const offlineTrack = personalTrack(703, { title: "离线合成曲目" });
  seedPersonalTracks(mock.personal, [localTrack, remoteTrack, offlineTrack]);
  mock.zones.seedLocal(localTrack);
  const remote = mock.zones.addDevice({ name: "客厅音箱", online: true, fixed, track: remoteTrack });
  const offline = mock.zones.addDevice({ name: "旧平板", online: false, track: offlineTrack });
  await page.goto("/settings/playback");
  await expect(bar(page).getByText(localTrack.title, { exact: true })).toBeVisible();
  await expect.poll(() => mock.calls.some((call) => call.path.endsWith("/output"))).toBe(true);
  return { remote, offline };
}

async function openPicker(page: Page) {
  // 窄屏（小于 768px）控制本机时迷你栏里没有设备按钮，设备在全屏播放器底部一排左侧，也只有图标。
  if ((page.viewportSize()?.width ?? 0) < 768) {
    await bar(page).getByRole("button", { name: "打开全屏播放器", exact: true }).click();
    const device = fullscreenDialog(page).getByRole("button", { name: "播放设备", exact: true });
    await expect(device).toHaveText("");
    await device.click();
  } else {
    await deviceButton(page).click();
  }
  await expect(picker(page)).toBeVisible();
  // 本机已经认领为在线输出，避免把启动中的本机误当成离线设备。
  await expect(picker(page).getByRole("button", { name: /测试浏览器 本机/ })).toBeVisible();
}

async function storedTarget(page: Page) {
  return page.evaluate((key) => {
    const text = localStorage.getItem(key);
    return text === null ? null : JSON.parse(text);
  }, TARGET_KEY);
}

function expectCommand(mock: ApiMock, zoneId: string, body: unknown) {
  expect(mock.calls).toContainEqual(expect.objectContaining({ method: "POST", path: `${BASE}/zones/${zoneId}/commands`, body }));
}

/** 全程只发 Tab；不借助 click、focus 或脚本挪动焦点。 */
async function tabTo(page: Page, target: Locator) {
  for (let index = 0; index < 100; index += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) return;
    await page.keyboard.press("Tab");
  }
  throw new Error("Tab 无法到达目标控件");
}

test("转移成功后控制目标跟到目标播放区，设备图标变成强调色，名称与悬停提示写出设备名", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { remote } = await setup(page, mock);
  await expect(deviceButton(page).locator("..")).not.toHaveAttribute("data-remote");
  const localColor = await deviceButton(page).locator("svg").evaluate((icon) => getComputedStyle(icon).color);
  await openPicker(page);
  await actions(page, "客厅音箱").getByRole("button", { name: "转到这台设备", exact: true }).click();
  await expect(picker(page)).not.toBeVisible();
  await expect(deviceButton(page)).toHaveAccessibleName("正在控制：客厅音箱");
  // 设备名不是可见文字：按钮里只有图标，外层带远端标记与右上角的小圆点，图标换成强调色。
  await expect(deviceButton(page)).toHaveText("");
  await expect(deviceButton(page).locator("..")).toHaveAttribute("data-remote", "");
  await expect.poll(() => deviceButton(page).locator("..").evaluate((wrapper) => getComputedStyle(wrapper, "::after").content)).toBe('""');
  expect(await deviceButton(page).locator("svg").evaluate((icon) => getComputedStyle(icon).color)).not.toBe(localColor);
  await deviceButton(page).hover();
  await expect(page.getByText("正在控制：客厅音箱", { exact: true })).toBeVisible();
  await expect(bar(page).getByText("本机合成曲目", { exact: true })).toBeVisible();
  expectCommand(mock, MOCK_ZONE, { command: "transfer", target_zone_id: remote.zoneId });
  expect(await storedTarget(page)).toEqual({ version: 1, zoneId: remote.zoneId, selectedAt: expect.any(Number) });
  await openPicker(page);
  await expect(picker(page).getByRole("button", { name: /客厅音箱/ })).toHaveAttribute("aria-pressed", "true");
});

test("离线设备的播放转到本机，控制目标回本机", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { offline } = await setup(page, mock);
  await openPicker(page);
  await picker(page).getByRole("button", { name: /客厅音箱/ }).click();
  await expect(deviceButton(page)).toHaveAccessibleName("正在控制：客厅音箱");
  await openPicker(page);
  await actions(page, "旧平板").getByRole("button", { name: "转到本机", exact: true }).click();
  await expect(picker(page)).not.toBeVisible();
  await expect(deviceButton(page)).toHaveAccessibleName("播放设备");
  await expect(deviceButton(page).locator("..")).not.toHaveAttribute("data-remote");
  await expect(bar(page).getByText("离线合成曲目", { exact: true })).toBeVisible();
  expectCommand(mock, offline.zoneId, { command: "transfer", target_zone_id: MOCK_ZONE });
  expect(await storedTarget(page)).toBeNull();
});

test("改名更新设备列表；离线设备删除须先确认队列一起删除", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { remote, offline } = await setup(page, mock);
  await openPicker(page);
  await actions(page, "客厅音箱").getByRole("button", { name: "改名", exact: true }).click();
  const rename = page.getByRole("dialog", { name: "改名", exact: true });
  await rename.getByRole("textbox", { name: "设备名称" }).fill("书房音箱");
  await rename.getByRole("button", { name: "保存", exact: true }).click();
  await expect(rename).not.toBeVisible();
  await expect(picker(page).getByRole("button", { name: /书房音箱/ })).toBeVisible();
  expect(mock.calls).toContainEqual(expect.objectContaining({ method: "PATCH", path: `${BASE}/devices/${remote.deviceId}`, body: { name: "书房音箱" } }));
  await actions(page, "旧平板").getByRole("button", { name: "删除", exact: true }).click();
  const confirm = page.getByRole("dialog", { name: "删除“旧平板”？", exact: true });
  await expect(confirm.getByText("它的队列会一起删除。", { exact: true })).toBeVisible();
  expect(mock.calls.filter((call) => call.method === "DELETE")).toHaveLength(0);
  await confirm.getByRole("button", { name: "取消", exact: true }).click();
  await expect(actions(page, "旧平板")).toBeVisible();
  expect(mock.calls.filter((call) => call.method === "DELETE")).toHaveLength(0);
  await actions(page, "旧平板").getByRole("button", { name: "删除", exact: true }).click();
  await confirm.getByRole("button", { name: "删除", exact: true }).click();
  await expect(confirm).not.toBeVisible();
  await expect(picker(page).getByText("旧平板", { exact: true })).toHaveCount(0);
  expect(mock.calls.filter((call) => call.method === "DELETE")).toEqual([
    expect.objectContaining({ path: `${BASE}/devices/${offline.deviceId}` }),
  ]);
});

test("设置标题旁显示控制目标设备名，各项改动向该播放区发送 set_settings", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { remote } = await setup(page, mock);
  await openPicker(page);
  await picker(page).getByRole("button", { name: /客厅音箱/ }).click();
  const heading = page.getByRole("heading", { name: "播放", exact: true, level: 1 });
  await expect(heading.locator("..").getByText("客厅音箱", { exact: true })).toBeVisible();
  // 服务端响应决定选中状态；点击后等待状态，不能要求 check() 同步改变受控输入。
  await page.getByRole("radio", { name: "按专辑", exact: true }).click();
  await expect(page.getByRole("radio", { name: "按专辑", exact: true })).toBeChecked();
  await page.getByRole("switch", { name: "无缝播放", exact: true }).click();
  await expect(page.getByRole("switch", { name: "无缝播放", exact: true })).not.toBeChecked();
  await page.getByRole("switch", { name: "交叉渐变", exact: true }).click();
  await expect(page.getByRole("radio", { name: "8 秒", exact: true })).toBeEnabled();
  await page.getByRole("radio", { name: "8 秒", exact: true }).click();
  await expect(page.getByRole("radio", { name: "8 秒", exact: true })).toBeChecked();
  await page.getByRole("radio", { name: "省流量", exact: true }).click();
  await expect(page.getByRole("radio", { name: "省流量", exact: true })).toBeChecked();
  const calls = mock.calls.filter((call) => call.path.endsWith("/commands"));
  expect(calls).toHaveLength(5);
  for (const settings of [{ leveling: "album" }, { gapless: false }, { crossfade: true }, { crossfade_seconds: 8 }, { quality: "lossy" }]) {
    expectCommand(mock, remote.zoneId, { command: "set_settings", settings });
  }
});

test("远端 volume fixed 时播放栏和全屏的音量及静音控件禁用", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await setup(page, mock, true);
  await openPicker(page);
  await picker(page).getByRole("button", { name: /客厅音箱/ }).click();
  await expect(bar(page).getByRole("slider", { name: "播放器音量" })).toBeDisabled();
  await expect(bar(page).getByRole("button", { name: "静音", exact: true })).toBeDisabled();
  await expect(bar(page).getByRole("slider", { name: "播放器音量" }).locator("..")).toHaveAttribute("title", "这台设备不能调音量");
  await bar(page).getByRole("button", { name: "打开全屏播放器", exact: true }).click();
  const fullscreen = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "收起全屏播放器", exact: true }) });
  await expect(fullscreen.getByRole("slider", { name: "播放器音量" })).toBeDisabled();
  await expect(fullscreen.getByRole("button", { name: "静音", exact: true })).toBeDisabled();
});

for (const width of widths) {
  test(`${width}px：设备列表打开时不横向溢出，axe 无严重问题`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    await setup(page, mock);
    await openPicker(page);
    await expect(picker(page).getByRole("listitem")).toHaveCount(3);
    await expectNoHorizontalOverflow(page, `设备列表 ${width}px`);
    // 顶层透明 dialog 铺满窗口，还须检查实际列表容器，不能只量 document。
    const bounds = await picker(page).getByRole("list").evaluate((list) => {
      const rect = list.getBoundingClientRect();
      return { left: rect.left, right: rect.right, viewport: innerWidth, overflow: list.scrollWidth - list.clientWidth };
    });
    expect(bounds.left).toBeGreaterThanOrEqual(0);
    expect(bounds.right).toBeLessThanOrEqual(bounds.viewport);
    expect(bounds.overflow).toBeLessThanOrEqual(0);
    await expectNoSeriousViolations(page, `设备列表 ${width}px`);
  });
}

test("纯键盘打开设备列表、跨行移动并转移，Esc 关闭后焦点回到按钮", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { remote } = await setup(page, mock);
  await tabTo(page, deviceButton(page));
  await page.keyboard.press("Enter");
  await expect(picker(page)).toBeVisible();
  await tabTo(page, picker(page).getByRole("button", { name: /测试浏览器 本机/ }));
  await expect(picker(page).getByRole("button", { name: /测试浏览器 本机/ })).toBeFocused();
  await tabTo(page, picker(page).getByRole("button", { name: /客厅音箱/ }));
  await expect(picker(page).getByRole("button", { name: /客厅音箱/ })).toBeFocused();
  await tabTo(page, actions(page, "客厅音箱").getByRole("button", { name: "转到这台设备", exact: true }));
  await page.keyboard.press("Enter");
  await expect(picker(page)).not.toBeVisible();
  await expect(deviceButton(page)).toHaveAccessibleName("正在控制：客厅音箱");
  expectCommand(mock, MOCK_ZONE, { command: "transfer", target_zone_id: remote.zoneId });
  await tabTo(page, deviceButton(page));
  await page.keyboard.press("Enter");
  await expect(picker(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(picker(page)).not.toBeVisible();
  await expect(deviceButton(page)).toBeFocused();
});

test("375px 迷你栏：控制本机时没有设备按钮，控制别的设备时在播放键左边出现设备图标，标题下没有设备名", async ({ page, backend }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  const mock = await backend.start({ signedInAs: "listener" });
  await setup(page, mock);
  await expect(deviceButton(page)).toBeHidden();
  await openPicker(page);
  await picker(page).getByRole("button", { name: /客厅音箱/ }).click();
  await expect(picker(page)).not.toBeVisible();
  // 全屏底部一排的设备按钮也换成远端名称，仍只有图标。
  const fullDevice = fullscreenDialog(page).getByRole("button", { name: "正在控制：客厅音箱", exact: true });
  await expect(fullDevice).toBeVisible();
  await expect(fullDevice).toHaveText("");
  await page.keyboard.press("Escape");
  await expect(fullscreenDialog(page)).not.toBeVisible();

  const device = deviceButton(page);
  await expect(device).toBeVisible();
  await expect(device).toHaveAccessibleName("正在控制：客厅音箱");
  await expect(device).toHaveText("");
  await expect(bar(page).getByText("客厅音箱", { exact: true })).toHaveCount(0);
  const deviceBox = await device.boundingBox();
  const playBox = await bar(page).getByRole("button", { name: /^(播放|暂停)$/ }).boundingBox();
  if (deviceBox === null || playBox === null) throw new Error("缺少设备或播放键");
  expect(deviceBox.x + deviceBox.width, "设备图标在播放键左边").toBeLessThanOrEqual(playBox.x + 1);
  expect(Math.abs((deviceBox.y + deviceBox.height / 2) - (playBox.y + playBox.height / 2)), "设备与播放键同一行").toBeLessThanOrEqual(4);
  await expectNoHorizontalOverflow(page, "迷你栏远端设备");
  await device.click();
  await expect(picker(page)).toBeVisible();
  await expectNoSeriousViolations(page, "迷你栏打开设备列表");
});

test("1440px 全屏：设备图标在右上角，设备列表在它下方展开、不超出窗口", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  await setup(page, mock);
  await bar(page).getByRole("button", { name: "打开全屏播放器", exact: true }).click();
  const full = fullscreenDialog(page);
  await expect(full.locator(":scope > div").first()).toHaveCSS("transform", "none");
  await expect(full.getByRole("button", { name: /^队列/ })).toHaveCount(0);
  const device = full.getByRole("button", { name: "播放设备", exact: true });
  const box = await device.boundingBox();
  if (box === null) throw new Error("缺少全屏设备按钮");
  expect(box.x, "设备在右上角").toBeGreaterThanOrEqual(1440 * 0.9);
  expect(box.y, "设备在右上角").toBeLessThanOrEqual(80);
  await device.click();
  await expect(picker(page)).toBeVisible();
  const list = await picker(page).getByRole("list").boundingBox();
  const content = await picker(page).locator(":scope > div").evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right };
  });
  if (list === null) throw new Error("缺少设备列表");
  expect(content.top, "列表在按钮下方").toBeGreaterThanOrEqual(box.y + box.height);
  expect(content.bottom).toBeLessThanOrEqual(900);
  expect(content.left).toBeGreaterThanOrEqual(0);
  expect(content.right).toBeLessThanOrEqual(1440);
  await page.keyboard.press("Escape");
  await expect(picker(page)).not.toBeVisible();
  await expect(device).toBeFocused();
});
