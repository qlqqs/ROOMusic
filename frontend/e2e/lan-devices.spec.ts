import { resolve } from "node:path";
import type { Locator } from "@playwright/test";
import { personalTrack } from "../src/test/personal-data";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { LAN_DEVICE_API, manualRenderer, rendererIp } from "./support/lan-devices";
import { heading } from "./support/locators";

const screenshots = resolve(import.meta.dirname, "../../temp/lan-device-shots");
const settingsPath = "/settings/devices";
const playbackApi = "/api/v1/me/playback/devices";
const rendererName = manualRenderer.name;

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

for (const width of [390, 1440]) {
  test(`${width}px：设置局域网开关、手动添加和分配成员`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/account");
    await page.getByRole("navigation", { name: "设置" }).getByRole("link", { name: "局域网设备" }).click();
    await expect(heading(page, "局域网设备")).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`${settingsPath}$`));
    const enabled = page.getByRole("switch", { name: "使用局域网里的播放设备" });
    const shared = page.getByRole("switch", { name: "所有成员共用" });
    await expect(enabled).not.toBeChecked();
    await expect(shared).toBeChecked();
    await enabled.click();
    await expect(enabled).toBeChecked();
    await expect(page.getByText(/还没有找到设备/)).toBeVisible();
    await shared.click();
    await expect(shared).not.toBeChecked();

    await page.getByRole("button", { name: "手动添加" }).click();
    const add = page.getByRole("dialog", { name: "手动添加设备" });
    await expectPanelFits(add);
    await add.getByRole("textbox", { name: "设备地址" }).fill(rendererIp);
    await add.getByRole("button", { name: "添加", exact: true }).click();
    await expect(add).toBeHidden();
    await expect(page.getByText(rendererName, { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `${rendererName} 的更多操作` }).click();
    await page.getByRole("menuitem", { name: `分配 ${rendererName} 给成员…` }).click();
    const grants = page.getByRole("dialog", { name: `哪些成员可以用“${rendererName}”` });
    const listener = grants.getByRole("checkbox", { name: "listener" });
    await expect(listener).not.toBeChecked();
    await expectPanelFits(grants);
    await listener.check();
    await grants.getByRole("button", { name: "保存" }).click();
    await expect(grants).toBeHidden();
    await expect(page.getByText(/分配给 1 位成员/)).toBeVisible();
    const member = mock.users.find((user) => user.username === "listener");
    expect(mock.callsTo("PUT", `${LAN_DEVICE_API}/settings`).map(({ body }) => body)).toEqual([{ enabled: true }, { shared: false }]);
    expect(mock.callsTo("POST", `${LAN_DEVICE_API}/devices`).map(({ body }) => body)).toEqual([{ kind: "upnp", address: rendererIp }]);
    expect(mock.callsTo("PUT", `${LAN_DEVICE_API}/devices/${encodeURIComponent(manualRenderer.udn)}/grants`).map(({ body }) => body))
      .toEqual([{ user_ids: [member?.id] }]);
    await expectNoHorizontalOverflow(page, `局域网设备设置 ${width}px`);
    await page.screenshot({ path: `${screenshots}/settings-${width}.png`, fullPage: true, animations: "disabled" });
  });

  test(`${width}px：SlimProto 开关、端口占用、播放器行和音质`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    mock.lanDevices.setSlimProtoUnavailable("port_in_use");
    const player = mock.lanDevices.addSlimProtoPlayer();
    const declared = player.capabilities.slimproto?.declared_max_sample_rate;
    expect(declared, "样本播放器声明 192 kHz").toBe(192000);
    await page.goto(settingsPath);
    await expect(heading(page, "局域网设备")).toBeVisible();
    await page.getByRole("switch", { name: "使用局域网里的播放设备" }).click();

    const slimSwitch = page.getByRole("switch", { name: "SlimProto", exact: true });
    await expect(slimSwitch).toBeChecked();
    await expect(slimSwitch).toHaveAccessibleDescription(/端口 3483 被本机其他程序占用/);
    await expect(page.getByText(/端口 3483 被本机其他程序占用（例如 Lyrion Music Server）/)).toBeVisible();
    await expect(page.locator("span").filter({ has: slimSwitch }).locator("svg.lucide-triangle-alert")).toBeVisible();

    const devices = page.getByRole("region", { name: "设备" });
    const row = devices.locator("div").filter({ has: page.getByText(player.name, { exact: true }) }).filter({ has: page.getByRole("button", { name: `${player.name} 的更多操作` }) }).last();
    await expect(row.getByText("SlimProto", { exact: true })).toBeVisible();
    await expect(row.getByText(/SqueezeLite 2\.0\.0 · FLAC，最高 192 kHz \/ 24 位/)).toBeVisible();
    await expect(row.getByText(/固定音量/)).toHaveCount(0);
    await page.evaluate(async () => { await document.fonts.ready; });
    await expectNoHorizontalOverflow(page, `SlimProto 设置 ${width}px`);
    await page.screenshot({ path: `${screenshots}/slimproto-settings-${width}.png`, fullPage: true, animations: "disabled" });

    await row.getByRole("button", { name: `${player.name} 的更多操作` }).click();
    await page.getByRole("menuitem", { name: `${player.name} 的音质…` }).click();
    const quality = page.getByRole("dialog", { name: `“${player.name}”的音质` });
    const rates = quality.getByRole("group", { name: "采样率上限（FLAC 无损）" });
    await expect(rates.getByRole("radio", { name: "跟随播放器（192 kHz）" })).toBeChecked();
    await expect(rates.getByRole("radio")).toHaveCount(7);
    for (const name of ["44.1 kHz", "48 kHz", "88.2 kHz", "96 kHz", "176.4 kHz", "192 kHz"]) await expect(rates.getByRole("radio", { name, exact: true })).toBeVisible();
    for (const name of ["352.8 kHz", "384 kHz", "705.6 kHz", "768 kHz"]) await expect(rates.getByRole("radio", { name, exact: true }), "不列出高于播放器声明值的采样率").toHaveCount(0);
    const volume = quality.getByRole("group", { name: "音量" });
    await expect(volume.getByRole("radio", { name: "在 ROOMusic 里调" })).toBeChecked();
    await rates.getByRole("radio", { name: "96 kHz", exact: true }).check();
    await volume.getByRole("radio", { name: "固定，在功放或解码器上调" }).check();
    await expectPanelFits(quality);
    await page.screenshot({ path: `${screenshots}/slimproto-quality-${width}.png`, animations: "disabled" });
    await quality.getByRole("button", { name: "保存" }).click();
    await expect(quality).toBeHidden();
    expect(mock.callsTo("PUT", `${LAN_DEVICE_API}/devices/${encodeURIComponent(player.udn)}/format`).map(({ body }) => body))
      .toEqual([{ codec: "flac", max_sample_rate: 96000, volume: "fixed" }]);
    await expect(row.getByText(/FLAC，最高 96 kHz \/ 24 位 · 固定音量，在功放或解码器上调/)).toBeVisible();

    await slimSwitch.click();
    await expect(slimSwitch).not.toBeChecked();
    await expect(slimSwitch).toHaveAccessibleDescription(/装了 Squeezelite 的播放器/);
    expect(mock.callsTo("PUT", `${LAN_DEVICE_API}/settings`).map(({ body }) => body)).toEqual([{ enabled: true }, { slimproto_enabled: false }]);
  });

  test(`${width}px：播放设备列表、接管和采样率上限`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    mock.zones.seedLocal(personalTrack(801, { title: "本机合成曲目" }));
    const curator = mock.users.find(({ username }) => username === "curator");
    if (curator === undefined) throw new Error("缺少测试管理员");
    const renderer = mock.zones.addDevice({
      name: rendererName,
      online: true,
      track: personalTrack(802, { title: "局域网合成曲目" }),
      upnp: { max_sample_rate: 48000, format_declared: false, occupied_by: { user_id: curator.id, display_name: curator.username } },
    });
    const deviceApi = `${playbackApi}/${renderer.deviceId}`;
    await page.goto("/settings/playback");
    const bar = page.getByRole("region", { name: "播放器", exact: true });
    await expect(bar.getByText("本机合成曲目", { exact: true })).toBeVisible();
    await page.evaluate(async () => { await document.fonts.ready; });
    const fullscreen = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "收起全屏播放器", exact: true }) });
    const player = width === 390 ? fullscreen : bar;
    if (width === 390) {
      await bar.getByRole("button", { name: "打开全屏播放器" }).click();
      await expect(fullscreen.locator(":scope > div").first()).toHaveCSS("transform", "none");
    }

    await player.getByRole("button", { name: "播放设备", exact: true }).click();
    const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
    const row = picker.getByRole("button", { name: new RegExp(rendererName) });
    await expect(row.getByText("局域网", { exact: true })).toBeVisible();
    await expect(row.getByText("curator 正在使用", { exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page, `播放设备 ${width}px`);
    await expectPanelFits(picker.getByRole("list"));
    await page.screenshot({ path: `${screenshots}/device-picker-${width}.png`, animations: "disabled" });

    await row.click();
    const takeover = page.getByRole("dialog", { name: `接管“${rendererName}”？` });
    await expect(takeover).toContainText("curator 正在使用这台设备");
    await expectPanelFits(takeover);
    expect(mock.callsTo("POST", `${deviceApi}/activate`)).toHaveLength(0);
    await takeover.getByRole("button", { name: "接管", exact: true }).click();
    await expect(picker).toBeHidden();
    expect(mock.callsTo("POST", `${deviceApi}/activate`).map(({ body }) => body)).toEqual([{ takeover: true }]);

    await player.getByRole("button", { name: `正在控制：${rendererName}` }).click();
    await expect(picker.getByRole("button", { name: new RegExp(rendererName), pressed: true })).toBeVisible();
    await expect(picker.getByText(/正在使用/)).toHaveCount(0);
    const openRate = picker.getByRole("group", { name: `${rendererName}的操作` }).getByRole("button", { name: "采样率上限" });
    await openRate.click();
    const rate = page.getByRole("dialog", { name: `“${rendererName}”的采样率上限` });
    await expect(rate.getByText(/没有说明它支持的格式/)).toBeVisible();
    await expect(rate.getByRole("radio", { name: "48 kHz", exact: true })).toBeChecked();
    await rate.getByRole("radio", { name: "96 kHz", exact: true }).check();
    await rate.getByRole("button", { name: "保存" }).click();
    await expect(rate).toBeHidden();
    expect(mock.callsTo("PUT", `${deviceApi}/sample-rate`).map(({ body }) => body)).toEqual([{ max_sample_rate: 96000 }]);
    await openRate.click();
    await expect(rate.getByRole("radio", { name: "96 kHz", exact: true })).toBeChecked();
    await expectPanelFits(rate);
  });
}

test("固定音量的局域网设备显示设备音量", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  mock.zones.addDevice({
    name: rendererName,
    online: true,
    fixed: true,
    track: personalTrack(803, { title: "固定音量合成曲目" }),
    upnp: { max_sample_rate: 48000, format_declared: true, occupied_by: null },
  });
  await page.goto("/settings/playback");
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  await bar.getByRole("button", { name: "播放设备", exact: true }).click();
  const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
  await picker.getByRole("button", { name: new RegExp(rendererName) }).click();
  await expect(bar.getByRole("button", { name: `正在控制：${rendererName}` })).toBeVisible();
  await expect(bar.getByRole("slider", { name: "播放器音量" })).toBeDisabled();
  expect(await bar.getByText("设备音量", { exact: true }).isVisible(), "固定音量时播放器应显示“设备音量”").toBe(true);
});
