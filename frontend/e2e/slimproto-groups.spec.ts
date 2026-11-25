import { resolve } from "node:path";
import type { Locator, Page } from "@playwright/test";
import { personalTrack } from "../src/test/personal-data";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";

const screenshots = resolve(import.meta.dirname, "../../temp/lan-device-shots");
const playbackApi = "/api/v1/me/playback";

async function expectPanelFits(panel: Locator) {
  await expect(panel).toBeVisible();
  const bounds = await panel.evaluate((element) => {
    const { left, right } = element.getBoundingClientRect();
    return { left, right, width: innerWidth, overflow: element.scrollWidth - element.clientWidth };
  });
  expect(bounds.left).toBeGreaterThanOrEqual(0);
  expect(bounds.right).toBeLessThanOrEqual(bounds.width);
  expect(bounds.overflow, "对话框或列表内容不得横向溢出").toBeLessThanOrEqual(0);
}

/** 宽屏在播放栏里打开设备列表；窄屏先打开全屏播放器。返回放着“播放设备”按钮的区域。 */
async function playerArea(page: Page, width: number): Promise<Locator> {
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  await expect(bar.getByText("本机合成曲目", { exact: true })).toBeVisible();
  if (width !== 390) return bar;
  const fullscreen = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "收起全屏播放器", exact: true }) });
  await bar.getByRole("button", { name: "打开全屏播放器" }).click();
  await expect(fullscreen.locator(":scope > div").first()).toHaveCSS("transform", "none");
  return fullscreen;
}

for (const width of [390, 1440]) {
  test(`${width}px：选用 SlimProto 设备、编组面板里接管加入一台、调同步微调、移出组`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    mock.zones.seedLocal(personalTrack(821, { title: "本机合成曲目" }));
    const curator = mock.users.find(({ username }) => username === "curator");
    if (curator === undefined) throw new Error("缺少测试管理员");
    const slim = (name: string, number: number, options: { online?: boolean; volume?: number; occupiedBy?: { user_id: string; display_name: string } } = {}) => mock.zones.addDevice({
      name, online: options.online ?? true, kind: "slimproto", ...(options.volume === undefined ? {} : { volume: options.volume }),
      track: personalTrack(number, { title: `${name}的合成曲目` }),
      upnp: { max_sample_rate: 48000, format_declared: true, occupied_by: options.occupiedBy ?? null },
    });
    const living = slim("客厅播放器", 822, { volume: 0.6 });
    const study = slim("书房播放器", 823, { volume: 0.4 });
    const bedroom = slim("卧室播放器", 824, { volume: 0.5, occupiedBy: { user_id: curator.id, display_name: curator.username } });
    slim("阳台播放器", 825, { online: false });
    // 重启后恢复的组：书房已在客厅的组里。
    mock.zones.seedGroup(living.zoneId, [{ deviceId: study.deviceId, state: "active" }]);
    const membersApi = `${playbackApi}/zones/${living.zoneId}/group/members`;

    await page.goto("/settings/playback");
    await page.evaluate(async () => { await document.fonts.ready; });
    const player = await playerArea(page, width);
    const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
    await player.getByRole("button", { name: "播放设备", exact: true }).click();
    await expect(picker.getByText("在「客厅播放器」的组里", { exact: true })).toBeVisible();
    await expect(picker.getByRole("button", { name: /书房播放器/ }), "成员行不能单独选用").toHaveCount(0);
    await picker.getByRole("button", { name: /客厅播放器/ }).click();
    await expect(picker).toBeHidden();
    expect(mock.callsTo("POST", `${playbackApi}/devices/${living.deviceId}/activate`).map(({ body }) => body)).toEqual([{ takeover: false }]);

    await player.getByRole("button", { name: "正在控制：客厅播放器" }).click();
    await expect(picker.getByRole("button", { name: /客厅播放器/, pressed: true })).toBeVisible();
    await picker.getByRole("group", { name: "客厅播放器的操作" }).getByRole("button", { name: "编组（2 台）" }).click();
    const panel = page.getByRole("dialog", { name: "编组：客厅播放器" });
    const members = panel.getByRole("region", { name: "组内设备" });
    const candidates = panel.getByRole("region", { name: "可以加入" });
    await expect(members.getByRole("listitem")).toHaveCount(2);
    await expect(members.getByRole("listitem").nth(0)).toContainText("主设备：客厅播放器");
    await expect(members.getByRole("listitem").nth(1)).toContainText("同步中：书房播放器");
    await expect(candidates.getByRole("button")).toHaveCount(1);
    await expect(candidates.getByText("curator 正在使用", { exact: true })).toBeVisible();

    // 别人在用：先确认接管，确认后才带 takeover 加入。
    await candidates.getByRole("button", { name: "加入“卧室播放器”" }).click();
    const takeover = page.getByRole("dialog", { name: "接管“卧室播放器”？" });
    await expect(takeover).toContainText("curator 正在使用这台设备");
    expect(mock.callsTo("POST", membersApi)).toHaveLength(0);
    await takeover.getByRole("button", { name: "接管", exact: true }).click();
    await expect(takeover).toBeHidden();
    expect(mock.callsTo("POST", membersApi).map(({ body }) => body)).toEqual([{ device_id: bedroom.deviceId, takeover: true }]);
    await expect(members.getByRole("listitem")).toHaveCount(3);
    await expect(members.getByRole("listitem").nth(2)).toContainText("加入中：卧室播放器");
    await expect(candidates.getByText("没有其他在线的 SlimProto 设备。")).toBeVisible();

    // 同步微调：回车保存；Esc 撤回未保存的输入且不关闭面板；越界时内联报错、不发请求。
    const delay = panel.getByRole("spinbutton", { name: "“卧室播放器”的同步微调（毫秒）" });
    await expect(delay).toHaveValue("0");
    await delay.fill("-35");
    await delay.press("Enter");
    await expect.poll(() => mock.callsTo("PUT", `${playbackApi}/devices/${bedroom.deviceId}/sync-delay`).map(({ body }) => body)).toEqual([{ sync_delay_ms: -35 }]);
    await expect(delay).toHaveValue("-35");
    await delay.fill("80");
    await delay.press("Escape");
    await expect(panel).toBeVisible();
    await expect(delay).toHaveValue("-35");
    await delay.fill("600");
    await delay.press("Enter");
    await expect(panel.getByText("取值 −500 到 500 毫秒。", { exact: true })).toBeVisible();
    await expect(delay).toHaveAttribute("aria-invalid", "true");
    await delay.press("Escape");
    await expect(delay).toHaveValue("-35");
    expect(mock.callsTo("PUT", `${playbackApi}/devices/${bedroom.deviceId}/sync-delay`)).toHaveLength(1);
    expect(mock.zones.deviceOf(bedroom.deviceId)?.sync_delay_ms).toBe(-35);

    await expectPanelFits(panel);
    await expectNoHorizontalOverflow(page, `编组面板 ${width}px`);
    await page.screenshot({ path: `${screenshots}/group-panel-${width}.png`, animations: "disabled" });

    await panel.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(panel).toBeHidden();
    await expect(picker.getByRole("group", { name: "客厅播放器的操作" }).getByRole("button", { name: "编组（3 台）" })).toBeVisible();
    await expect(picker.getByText("在「客厅播放器」的组里", { exact: true })).toHaveCount(2);
    await expectPanelFits(picker.getByRole("list"));
    await expectNoHorizontalOverflow(page, `播放设备（编组） ${width}px`);
    await page.screenshot({ path: `${screenshots}/group-device-list-${width}.png`, animations: "disabled" });

    // 在设备列表里把书房移出组：它回到自己的播放区，可以单独选用。
    await picker.getByRole("group", { name: "书房播放器的操作" }).getByRole("button", { name: "移出组" }).click();
    await expect(picker.getByRole("button", { name: /书房播放器/ })).toBeVisible();
    expect(mock.callsTo("DELETE", `${membersApi}/${study.deviceId}`)).toHaveLength(1);
    await expect(picker.getByRole("group", { name: "客厅播放器的操作" }).getByRole("button", { name: "编组（2 台）" })).toBeVisible();
  });
}

test("有组时播放栏的音量控件调组音量", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  mock.zones.seedLocal(personalTrack(831, { title: "本机合成曲目" }));
  const slim = (name: string, number: number, volume: number) => mock.zones.addDevice({
    name, online: true, kind: "slimproto", volume, track: personalTrack(number, { title: `${name}的合成曲目` }),
    upnp: { max_sample_rate: 48000, format_declared: true, occupied_by: null },
  });
  const living = slim("客厅播放器", 832, 0.3);
  const study = slim("书房播放器", 833, 0.6);
  mock.zones.seedGroup(living.zoneId, [{ deviceId: study.deviceId, state: "active" }]);
  await page.goto("/settings/playback");
  const bar = await playerArea(page, 1440);
  await bar.getByRole("button", { name: "播放设备", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
  await picker.getByRole("button", { name: /客厅播放器/ }).click();
  await expect(bar.getByRole("button", { name: "正在控制：客厅播放器" })).toBeVisible();
  const slider = bar.getByRole("slider", { name: "组音量" });
  await expect(slider, "显示组内最响的一台").toHaveValue("0.6");
  await slider.fill("0.3");
  await expect.poll(() => mock.callsTo("PUT", `${playbackApi}/zones/${living.zoneId}/group/volume`).map(({ body }) => body)).toEqual([{ volume: 0.3 }]);
  expect(mock.callsTo("PUT", /\/devices\/[^/]+\/volume$/)).toHaveLength(0);
  // 服务端按比例缩放：客厅 0.3 → 0.15，书房 0.6 → 0.3。
  expect([mock.zones.deviceOf(living.deviceId)?.volume, mock.zones.deviceOf(study.deviceId)?.volume]).toEqual([0.15, 0.3]);
});
