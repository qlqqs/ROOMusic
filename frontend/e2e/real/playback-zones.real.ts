import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { expect, test, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import type { ZoneCommand } from "../../src/features/player/zones/api";
import { decode as d } from "../../src/shared/api";
import { body, catalog, decodeError, decodeID, login, observeMedia, origin, post, prepareLibrary, scan, type Albums } from "./support";

// 选择器操作最多等待 10 秒；真实播放与重启仍使用各断言明确指定的等待期限。
test.use({ actionTimeout: 10_000 });

// Node 中只读验收需要的字段，避免业务 decoder 经组件入口加载 CSS；完整 DTO 映射由 Vitest 覆盖。
const base = "/api/v1/me/playback";
const entryDecoder = d.object({ entry_id: d.uuid(), track: d.object({ track_id: d.uuid() }) });
const streamDecoder = d.object({
  seq: d.integer(), url: d.string(), quality: d.string(), mime_type: d.string(),
  format: d.nullable(d.object({ codec: d.string(), sample_rate: d.integer(), bit_depth: d.nullable(d.integer()), channels: d.integer() })),
  segments: d.array(d.object({
    entry_id: d.uuid(), track: d.object({ track_id: d.uuid() }), stream_start_seconds: d.number(), track_start_seconds: d.number(),
  }), { max: 10_000 }),
});
const zoneDecoder = d.object({
  zone_id: d.uuid(), intent: d.oneOf(["play", "pause"] as const), status: d.string(),
  queue_length: d.integer(), mode: d.string(), current: d.nullable(entryDecoder),
  settings: d.object({ leveling: d.string(), gapless: d.boolean(), crossfade: d.boolean(), crossfade_seconds: d.number(), quality: d.string() }),
  position: d.object({ seconds: d.number(), at: d.string() }), stream: d.nullable(streamDecoder),
  signal: d.nullable(d.object({
    quality: d.string(), steps: d.array(d.object({
      kind: d.string(), lossy: d.boolean(),
      params: d.object({ codec: d.optional(d.string()), gain_db: d.optional(d.number()), basis: d.optional(d.string()) }),
    }), { max: 64 }),
  })),
});
const decodeSnapshot = d.defineDecoder(d.object({
  devices: d.array(d.object({ device_id: d.uuid(), zone_id: d.uuid(), name: d.string(), online: d.boolean(), output_session_id: d.nullable(d.uuid()) }), { max: 10_000 }),
  zones: d.array(zoneDecoder, { max: 10_000 }),
}));
const decodeQueue = d.defineDecoder(d.object({ entries: d.array(entryDecoder, { max: 10_000 }) }));
const decodeCommand = d.defineDecoder(d.object({ zone: zoneDecoder }));
const decodeHistory = d.defineDecoder(d.object({
  items: d.array(d.object({ track_id: d.uuid(), play_count: d.integer() }), { max: 100 }),
}));
const decodeAnalysis = d.defineDecoder(d.object({
  status: d.string(), analysis: d.nullable(d.object({ track_gain_db: d.nullable(d.number()) })),
}));
type Zone = d.Infer<typeof zoneDecoder>;
type Device = ReturnType<typeof decodeSnapshot>["devices"][number];
const bar = (page: Page) => page.getByRole("region", { name: "播放器", exact: true });
const snapshot = async (api: APIRequestContext) => decodeSnapshot(await body(await api.get(base)));
const queue = async (api: APIRequestContext, zoneId: string) => decodeQueue(await body(await api.get(`${base}/zones/${zoneId}/queue`))).entries;

async function zone(api: APIRequestContext, id: string): Promise<Zone> {
  const value = (await snapshot(api)).zones.find((item) => item.zone_id === id);
  if (value === undefined) throw new Error(`快照缺少播放区 ${id}`);
  return value;
}

async function command(api: APIRequestContext, zoneId: string, data: ZoneCommand) {
  return decodeCommand(await body(await api.post(`${base}/zones/${zoneId}/commands`, { data, headers: { Origin: origin } }))).zone;
}

function trackNamed(albums: Albums, title: string) {
  for (const album of albums) {
    const track = album.release.media.flatMap((medium) => medium.tracks).find((item) => item.title === title);
    if (track !== undefined) return { ...album, track };
  }
  throw new Error(`缺少隔离脚本素材：${title}`);
}

/** 每个用例独立成员，避免离线设备、队列、历史污染其他验收。管理员只负责扫描与创建成员。 */
async function member(api: APIRequestContext) {
  const credentials = { username: `zones-${crypto.randomUUID().slice(0, 12)}`, password: "PlaybackFixturePassword42" };
  const result = decodeID(await body(await post(api, "/api/v1/users", credentials)));
  return { credentials, id: result.id };
}

let fixtureAlbums: Albums | undefined;
async function fixtures(api: APIRequestContext) {
  await prepareLibrary(api);
  // 素材整轮不变：每个 worker 只扫描一次；失败后 Playwright 换 worker 时仍能独立重建前提。
  if (fixtureAlbums === undefined) {
    await scan(api);
    fixtureAlbums = await catalog(api);
  }
  return fixtureAlbums;
}

/** 先登记并认领，再点击可见标题；远程起播不能依赖禁用自动播放策略。 */
async function openOutput(page: Page, name: string) {
  const known = new Set((await snapshot(page.request)).devices.map((item) => item.device_id));
  await observeMedia(page);
  await page.goto("/albums");
  await expect.poll(async () => (await snapshot(page.request)).devices.filter((item) => !known.has(item.device_id) && item.online && item.output_session_id !== null).length).toBe(1);
  const device = (await snapshot(page.request)).devices.find((item) => !known.has(item.device_id));
  if (device === undefined) throw new Error("浏览器没有登记输出设备");
  await page.getByRole("heading", { name: "专辑", exact: true }).click();
  // 单设备且队列为空时没有播放栏；改名只是测试前置，通过公开接口完成。
  await body(await page.request.patch(`${base}/devices/${device.device_id}`, { data: { name }, headers: { Origin: origin } }));
  await expect.poll(async () => (await snapshot(page.request)).devices.find((item) => item.device_id === device.device_id)?.name).toBe(name);
  return { ...device, name };
}

async function selectDevice(page: Page, name: string) {
  await bar(page).getByRole("button", { name: /^播放设备/ }).click();
  const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
  await picker.getByRole("button").filter({ has: page.getByText(name, { exact: true }) }).click();
  await expect(picker).toBeHidden();
}

async function transferTo(page: Page, name: string, offline = false) {
  await bar(page).getByRole("button", { name: /^播放设备/ }).click();
  const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
  const row = picker.getByRole("listitem").filter({ has: page.getByText(name, { exact: true }) });
  if (offline) await expect(row.getByText("离线", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: offline ? "转到本机" : "转到这台设备", exact: true }).click();
  await expect(picker).toBeHidden();
}

async function startFromAlbum(page: Page, item: ReturnType<typeof trackNamed>) {
  await page.goto(`/albums/${item.groupId}`);
  const row = page.locator("ol > li").filter({ has: page.getByText(item.track.title, { exact: true }) });
  await row.getByRole("button", { name: "从这首开始播放", exact: true }).click();
}

/** 真实鼠标操作原生进度条，不写 DOM value、不调用内部 controller。 */
async function seekFraction(slider: Locator, fraction: number) {
  await expect(slider).toBeEnabled();
  const bounds = await slider.boundingBox();
  if (bounds === null) throw new Error("播放进度条不可见");
  await slider.click({ position: { x: bounds.width * fraction, y: bounds.height / 2 } });
}

async function nativePosition(page: Page, state: Zone) {
  const audio = (await sounding(page))[0];
  if (audio === undefined || state.stream === null) throw new Error("没有可观察的渲染流");
  expect(new URL(audio.src).pathname).toBe(state.stream.url);
  const segment = state.stream.segments.filter((item) => item.stream_start_seconds <= audio.seconds).at(-1);
  if (segment === undefined) throw new Error("当前媒体位置没有对应的流分段");
  return segment.track_start_seconds + audio.seconds - segment.stream_start_seconds;
}

async function expectTransferred(page: Page, api: APIRequestContext, source: Device, target: Device, expectedQueue: Awaited<ReturnType<typeof queue>>, position: number) {
  await expect.poll(async () => (await sounding(page)).length).toBe(1);
  await expect.poll(async () => (await zone(api, source.zone_id)).queue_length).toBe(0);
  expect(await queue(api, source.zone_id)).toEqual([]);
  expect(await zone(api, source.zone_id)).toMatchObject({ intent: "pause", current: null });
  expect(await queue(api, target.zone_id)).toEqual(expectedQueue);
  const resumed = await zone(api, target.zone_id);
  expect(resumed.intent).toBe("play");
  const start = resumed.stream?.segments[0]?.track_start_seconds;
  expect(start).toBeGreaterThanOrEqual(position - 2);
  expect(start).toBeLessThanOrEqual(position + 5);
}

/** 只观察原生媒体：排除手势解锁用的静音片段，不替换播放、解码或时间推进。 */
async function sounding(page: Page) {
  return page.evaluate(() => window.playbackTestMedia
    .filter((audio) => /\/api\/v1\/me\/playback\/zones\/[^/]+\/streams\//.test(audio.currentSrc)
      && !audio.paused && !audio.ended && audio.readyState >= 2 && !audio.muted && audio.volume > 0)
    .map((audio) => ({ src: audio.currentSrc, seconds: audio.currentTime })));
}

test("同一浏览器两个标签页只有一个出声，关闭出声页后另一页从原位置接着放", async ({ page, context }) => {
  const albums = await fixtures(context.request);
  const album = albums.find((item) => item.release.media.some((medium) => medium.tracks.some((track) => track.title === "Slow Revocation")));
  const track = album?.release.media.flatMap((medium) => medium.tracks).find((item) => item.title === "Slow Revocation");
  if (album === undefined || track === undefined) throw new Error("缺少隔离脚本生成的 180 秒合成素材");
  const snapshot = async () => decodeSnapshot(await body(await context.request.get("/api/v1/me/playback")));
  const bar = (tab: Page) => tab.getByRole("region", { name: "播放器", exact: true });

  await observeMedia(page);
  await page.goto(`/albums/${album.groupId}`);
  // 先让第一页取得输出权，再打开控制页；这条用例只验证单一出声与关闭接手，不扩大到同时登记。
  await expect.poll(async () => (await snapshot()).devices.filter((device) => device.online && device.output_session_id !== null).length).toBe(1);
  const initial = await snapshot();
  const device = initial.devices[0];
  if (device === undefined || device.output_session_id === null) throw new Error("第一页没有取得输出会话");

  const other = await context.newPage();
  try {
    await observeMedia(other);
    await other.goto(`/albums/${album.groupId}`);
    // 两页均有真实用户手势，避免把自动播放策略当作接手协议的一部分。
    await other.getByRole("heading", { name: album.release.title, exact: true }).click();
    const row = page.locator("ol > li").filter({ has: page.getByText(track.title, { exact: true }) });
    await row.getByRole("button", { name: "从这首开始播放", exact: true }).click();
    await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0).toBeGreaterThan(1);
    await expect(bar(other).getByText("正在播放", { exact: true })).toBeVisible();
    expect((await sounding(page)).length + (await sounding(other)).length).toBe(1);
    expect(await sounding(other)).toEqual([]);
    const beforeClose = (await sounding(page))[0];
    if (beforeClose === undefined) throw new Error("第一页没有原生媒体正在播放");
    expect((await snapshot()).devices).toHaveLength(1);

    const closed = page.waitForEvent("close");
    await page.close({ runBeforeUnload: true });
    await closed;
    await expect.poll(async () => (await sounding(other))[0]?.seconds ?? 0).toBeGreaterThan(0);
    expect(await sounding(other)).toHaveLength(1);
    await expect.poll(async () => {
      const resumed = await snapshot();
      return resumed.devices.find((item) => item.device_id === device.device_id)?.output_session_id;
    }).not.toBe(device.output_session_id);
    const resumed = await snapshot();
    const zone = resumed.zones.find((item) => item.zone_id === device.zone_id);
    expect(resumed.devices).toHaveLength(1);
    expect(resumed.devices[0]?.online).toBe(true);
    expect(zone?.intent).toBe("play");
    expect(zone?.current?.track.track_id).toBe(track.id);
    expect(zone?.stream?.url).not.toBe(new URL(beforeClose.src).pathname);
    // 接手开的是新流，但曲目内起点保留关闭页回报的位置，不能从头播放。
    expect(zone?.stream?.segments[0]?.track_start_seconds).toBeGreaterThanOrEqual(beforeClose.seconds - 0.5);
    await expect(bar(other).getByText("正在播放", { exact: true })).toBeVisible();
  } finally {
    await other.close();
  }
});

test("手机远程控制电脑：进度、暂停、切歌与定位生效，转移清空源，离线后仍可接手", async ({ browser, request }) => {
  test.setTimeout(100_000);
  const albums = await fixtures(request);
  const account = await member(request);
  const slow = trackNamed(albums, "Slow Revocation");
  const alac = trackNamed(albums, "Zone ALAC");
  const desktop = await browser.newContext({ baseURL: origin, viewport: { width: 1440, height: 900 } });
  const mobile = await browser.newContext({ baseURL: origin, viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });
  try {
    await login(desktop.request, account.credentials);
    await login(mobile.request, account.credentials);
    const computer = await desktop.newPage();
    const phone = await mobile.newPage();
    const pc = await openOutput(computer, "验收电脑");
    const handset = await openOutput(phone, "验收手机");

    await test.step("手机选中电脑并起播，手机进度跟随但不出声", async () => {
      await selectDevice(phone, pc.name);
      await startFromAlbum(phone, slow);
      await command(mobile.request, pc.zone_id, { command: "enqueue", track_ids: [alac.track.id, slow.track.id] });
      await expect.poll(async () => (await sounding(computer))[0]?.seconds ?? 0).toBeGreaterThan(1);
      expect(await sounding(phone)).toEqual([]);
      await expect(bar(phone).getByRole("button", { name: `播放设备：${pc.name}`, exact: true })).toBeVisible();
      await bar(phone).getByRole("button", { name: "打开全屏播放器" }).click();
      const fullscreen = phone.getByRole("dialog", { name: slow.track.title, exact: true });
      const progress = fullscreen.getByRole("slider", { name: "播放进度" });
      const before = Number(await progress.inputValue());
      await expect.poll(async () => Number(await progress.inputValue())).toBeGreaterThan(before + 0.5);
      await fullscreen.getByRole("button", { name: "暂停", exact: true }).click();
      await expect.poll(async () => (await sounding(computer)).length).toBe(0);
      await expect.poll(async () => (await zone(mobile.request, pc.zone_id)).intent).toBe("pause");
      await expect(bar(computer).getByText("已暂停", { exact: true })).toBeVisible();
      await fullscreen.getByRole("button", { name: "播放", exact: true }).click();
      await expect.poll(async () => (await sounding(computer)).length).toBe(1);
      await fullscreen.getByRole("button", { name: "下一首", exact: true }).click();
      await expect(phone.getByRole("dialog", { name: alac.track.title, exact: true })).toBeVisible();
      await expect(bar(computer).getByText(alac.track.title, { exact: true })).toBeVisible();
      await expect.poll(async () => (await zone(mobile.request, pc.zone_id)).current?.track.track_id).toBe(alac.track.id);
      await phone.getByRole("dialog", { name: alac.track.title, exact: true }).getByRole("button", { name: "下一首", exact: true }).click();
      await expect(fullscreen).toBeVisible();
      await fullscreen.getByRole("button", { name: "暂停", exact: true }).click();
      await expect.poll(async () => (await sounding(computer)).length).toBe(0);
      await seekFraction(progress, 0.25);
      await expect.poll(async () => (await zone(mobile.request, pc.zone_id)).position.seconds).toBeGreaterThan(40);
      expect((await zone(mobile.request, pc.zone_id)).position.seconds).toBeLessThan(50);
      await fullscreen.getByRole("button", { name: "播放", exact: true }).click();
      await expect.poll(async () => (await zone(mobile.request, pc.zone_id)).stream?.segments[0]?.track_start_seconds ?? 0).toBeGreaterThan(40);
      const soughtStream = (await zone(mobile.request, pc.zone_id)).stream?.url;
      await expect.poll(async () => new URL((await sounding(computer))[0]?.src ?? origin).pathname).toBe(soughtStream);
      await expect.poll(async () => (await sounding(computer))[0]?.seconds ?? 0).toBeGreaterThan(1);
      expect(await nativePosition(computer, await zone(mobile.request, pc.zone_id))).toBeGreaterThan(40);
      expect(await sounding(phone)).toEqual([]);
      await fullscreen.getByRole("button", { name: "收起全屏播放器" }).click();
      await expect(fullscreen).toBeHidden();
    });

    await test.step("正在播放的队列与原位置转到手机，电脑停声且队列为空", async () => {
      const entries = await queue(mobile.request, pc.zone_id);
      const before = await nativePosition(computer, await zone(mobile.request, pc.zone_id));
      await transferTo(phone, handset.name);
      await expectTransferred(phone, mobile.request, pc, handset, entries, before);
      await expect.poll(async () => (await sounding(computer)).length).toBe(0);
      await expect(bar(computer).getByRole("button", { name: "队列（0）", exact: true })).toBeVisible();
      expect((await zone(mobile.request, handset.zone_id)).current?.entry_id).toBe(entries[2]?.entry_id);
    });

    await test.step("转回电脑后关闭输出页，手机显示离线并从保留位置继续", async () => {
      await transferTo(phone, pc.name);
      await expect.poll(async () => (await sounding(computer)).length).toBe(1);
      await expect.poll(async () => (await sounding(phone)).length).toBe(0);
      const before = await nativePosition(computer, await zone(mobile.request, pc.zone_id));
      const entries = await queue(mobile.request, pc.zone_id);
      const closed = computer.waitForEvent("close");
      await computer.close({ runBeforeUnload: true });
      await closed;
      await expect.poll(async () => (await snapshot(mobile.request)).devices.find((item) => item.device_id === pc.device_id)?.online).toBe(false);
      const offline = await zone(mobile.request, pc.zone_id);
      expect(offline.intent).toBe("pause");
      expect(offline.position.seconds).toBeGreaterThanOrEqual(before - 2);
      expect(await queue(mobile.request, pc.zone_id)).toEqual(entries);
      await transferTo(phone, pc.name, true);
      await expectTransferred(phone, mobile.request, pc, handset, entries, offline.position.seconds);
      await expect(bar(phone).getByText(slow.track.title, { exact: true })).toBeVisible();
    });
  } finally {
    await Promise.all([desktop.close(), mobile.close()]);
  }
});

test("真实服务重启保留两个播放区的队列和位置，只让此前正在播放的输出恢复", async ({ browser, request }) => {
  test.setTimeout(100_000);
  const restartScript = process.env["ROOMUSIC_PLAYBACK_RESTART_SCRIPT"];
  if (!restartScript) throw new Error("缺少 ROOMUSIC_PLAYBACK_RESTART_SCRIPT，不能以跳过代替真实重启验收");
  const albums = await fixtures(request);
  const account = await member(request);
  const slow = trackNamed(albums, "Slow Revocation");
  const first = trackNamed(albums, "Zone First");
  const desktop = await browser.newContext({ baseURL: origin });
  const mobile = await browser.newContext({ baseURL: origin, viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });
  try {
    await login(desktop.request, account.credentials);
    await login(mobile.request, account.credentials);
    const computer = await desktop.newPage();
    const phone = await mobile.newPage();
    const pc = await openOutput(computer, "重启前播放的电脑");
    const handset = await openOutput(phone, "重启前暂停的手机");
    for (const device of [pc, handset]) {
      await command(desktop.request, device.zone_id, { command: "play_tracks", track_ids: [slow.track.id, first.track.id], start_index: 0 });
    }
    await expect.poll(async () => (await sounding(computer)).length).toBe(1);
    await expect.poll(async () => (await sounding(phone)).length).toBe(1);
    await bar(phone).getByRole("button", { name: "暂停", exact: true }).click();
    await expect.poll(async () => (await sounding(phone)).length).toBe(0);
    await command(desktop.request, handset.zone_id, { command: "seek", position_seconds: 42 });
    await command(desktop.request, pc.zone_id, { command: "seek", position_seconds: 30 });
    await expect.poll(async () => (await zone(desktop.request, pc.zone_id)).stream?.segments[0]?.track_start_seconds).toBe(30);
    const seekStream = (await zone(desktop.request, pc.zone_id)).stream?.url;
    await expect.poll(async () => new URL((await sounding(computer))[0]?.src ?? origin).pathname).toBe(seekStream);
    await expect.poll(async () => (await sounding(computer))[0]?.seconds ?? 0).toBeGreaterThan(1);
    const playingBefore = await zone(desktop.request, pc.zone_id);
    const pausedBefore = await zone(desktop.request, handset.zone_id);
    const pcQueue = await queue(desktop.request, pc.zone_id);
    const phoneQueue = await queue(desktop.request, handset.zone_id);
    const before = await nativePosition(computer, playingBefore);
    expect(pausedBefore).toMatchObject({ intent: "pause", position: { seconds: 42 } });
    const started = Date.now();
    await test.step("调用隔离脚本停止并启动真实服务，不刷新页面、不手动重播", async () => {
      await promisify(execFile)("bash", [restartScript], { timeout: 45_000 });
    });
    await test.step("新输出会话自动恢复，暂停区不被误启动", async () => {
      for (const device of [pc, handset]) {
        await expect.poll(async () => {
          const current = (await snapshot(desktop.request)).devices.find((item) => item.device_id === device.device_id);
          return current?.online === true && current.output_session_id !== null && current.output_session_id !== device.output_session_id;
        }, { timeout: 35_000 }).toBe(true);
      }
      await expect.poll(async () => (await zone(desktop.request, pc.zone_id)).intent, { timeout: 15_000 }).toBe("play");
      await expect.poll(async () => (await sounding(computer)).length).toBe(1);
      const resumed = await zone(desktop.request, pc.zone_id);
      expect(resumed.current).toEqual(playingBefore.current);
      expect(resumed.stream?.seq).toBeGreaterThan(playingBefore.stream?.seq ?? 0);
      expect(resumed.stream?.segments[0]?.track_start_seconds).toBeGreaterThanOrEqual(before - 2);
      expect(resumed.stream?.segments[0]?.track_start_seconds).toBeLessThanOrEqual(before + (Date.now() - started) / 1000 + 5);
      expect(await queue(desktop.request, pc.zone_id)).toEqual(pcQueue);
      expect(await queue(desktop.request, handset.zone_id)).toEqual(phoneQueue);
      expect(await zone(desktop.request, handset.zone_id)).toMatchObject({ intent: "pause", current: pausedBefore.current, position: { seconds: 42 } });
      expect(await sounding(phone)).toEqual([]);
      await expect(bar(computer).getByText("正在播放", { exact: true })).toBeVisible();
      await expect(bar(phone).getByRole("button", { name: "播放", exact: true })).toBeVisible();
      // 继续观察一段真实媒体推进，避免仅在重新认领瞬间检查暂停区。
      const after = (await sounding(computer))[0]?.seconds ?? 0;
      await expect.poll(async () => (await sounding(computer))[0]?.seconds ?? 0).toBeGreaterThan(after + 1);
      expect(await sounding(phone)).toEqual([]);
      expect((await zone(desktop.request, handset.zone_id)).intent).toBe("pause");
    });
  } finally {
    await Promise.all([desktop.close(), mobile.close()]);
  }
});

test("成员只看见自己的设备，跨成员读取队列、控制、转移和读取媒体均被拒绝", async ({ browser, request }) => {
  const albums = await fixtures(request);
  const a = await member(request);
  const b = await member(request);
  const slow = trackNamed(albums, "Slow Revocation");
  const first = trackNamed(albums, "Zone First");
  const contextA = await browser.newContext({ baseURL: origin });
  const contextB = await browser.newContext({ baseURL: origin });
  try {
    await login(contextA.request, a.credentials);
    await login(contextB.request, b.credentials);
    const pageA = await contextA.newPage();
    const pageB = await contextB.newPage();
    const deviceA = await openOutput(pageA, "成员甲的私有电脑");
    const deviceB = await openOutput(pageB, "成员乙的私有手机");
    // 成员乙看不到甲的设备；给乙自己的空播放区入队但不起播，才有设备列表入口。
    const paused = await command(contextB.request, deviceB.zone_id, { command: "enqueue", track_ids: [first.track.id] });
    expect(paused).toMatchObject({ intent: "pause", queue_length: 1, current: { track: { track_id: first.track.id } } });
    expect(await sounding(pageB)).toEqual([]);
    await command(contextA.request, deviceA.zone_id, { command: "play_tracks", track_ids: [slow.track.id], start_index: 0 });
    await expect.poll(async () => (await sounding(pageA)).length).toBe(1);
    const ownerZone = await zone(contextA.request, deviceA.zone_id);
    if (ownerZone.stream === null) throw new Error("成员甲未取得渲染流");

    for (const [page, own, foreign] of [[pageA, deviceA, deviceB], [pageB, deviceB, deviceA]] as const) {
      await test.step(`${own.name} 的界面与接口隔离`, async () => {
        const state = await snapshot(page.request);
        expect(state.devices.map((item) => item.device_id)).toEqual([own.device_id]);
        expect(state.zones.map((item) => item.zone_id)).toEqual([own.zone_id]);
        await bar(page).getByRole("button", { name: /^播放设备/ }).click();
        const picker = page.getByRole("dialog", { name: "播放设备", exact: true });
        await expect(picker.getByText(own.name, { exact: true })).toBeVisible();
        await expect(picker.getByText(foreign.name, { exact: true })).toHaveCount(0);
        await expect(picker.getByRole("listitem")).toHaveCount(1);
        await picker.getByRole("button", { name: "关闭", exact: true }).click();
        const attempts = [
          await page.request.get(`${base}/zones/${foreign.zone_id}/queue`),
          await page.request.post(`${base}/zones/${foreign.zone_id}/commands`, { data: { command: "pause" }, headers: { Origin: origin } }),
          await page.request.post(`${base}/zones/${own.zone_id}/commands`, { data: { command: "transfer", target_zone_id: foreign.zone_id }, headers: { Origin: origin } }),
          await page.request.patch(`${base}/devices/${foreign.device_id}`, { data: { name: "越权改名" }, headers: { Origin: origin } }),
          await page.request.put(`${base}/devices/${foreign.device_id}/volume`, { data: { volume: 0, muted: true }, headers: { Origin: origin } }),
        ];
        for (const response of attempts) {
          expect(response.status(), response.url()).toBe(404);
          const input: unknown = await response.json();
          expect(decodeError(input).error.code).toBe("not_found");
        }
      });
    }
    const response = await contextB.request.get(ownerZone.stream.url);
    expect(response.status()).toBe(404);
    const input: unknown = await response.json();
    expect(decodeError(input).error.code).toBe("not_found");
    expect((await zone(contextA.request, deviceA.zone_id)).intent).toBe("play");
    expect(await sounding(pageA)).toHaveLength(1);
    expect(await sounding(pageB)).toEqual([]);
  } finally {
    await Promise.all([contextA.close(), contextB.close()]);
  }
});

test("升级首次打开导入旧 sessionStorage 队列、当前项、模式与位置，刷新不重复导入", async ({ page, context, request }) => {
  const albums = await fixtures(request);
  const account = await member(request);
  await login(context.request, account.credentials);
  const first = trackNamed(albums, "Zone First");
  const slow = trackNamed(albums, "Slow Revocation");
  const key = `roomusic:player:v1:${account.id}`;
  const saved = {
    version: 1,
    entries: [first, slow].map((item, index) => ({ entryId: `legacy-${index}`, trackId: item.track.id, releaseId: item.release.id, groupId: item.groupId })),
    currentEntryId: "legacy-1", mode: "repeat_all", positionSeconds: 42, history: ["legacy-0"],
  };
  await page.addInitScript(({ key, saved }) => {
    // 仅预置升级前持久化格式；不写应用 store，也不拦截导入接口。
    if (sessionStorage.getItem("zone-e-legacy-seeded") === null) {
      sessionStorage.setItem(key, JSON.stringify(saved));
      sessionStorage.setItem("zone-e-legacy-seeded", "true");
    }
  }, { key, saved });
  const device = await openOutput(page, "迁移后的本机");
  await expect.poll(async () => (await zone(context.request, device.zone_id)).queue_length).toBe(2);
  const state = await zone(context.request, device.zone_id);
  expect(state).toMatchObject({ intent: "pause", mode: "repeat_all", current: { track: { track_id: slow.track.id } }, position: { seconds: 42 } });
  const entries = await queue(context.request, device.zone_id);
  expect(entries.map((item) => item.track.track_id)).toEqual([first.track.id, slow.track.id]);
  expect(state.current?.entry_id).toBe(entries[1]?.entry_id);
  expect(await sounding(page)).toEqual([]);
  await expect.poll(() => page.evaluate((key) => sessionStorage.getItem(key), key)).toBeNull();
  await bar(page).getByRole("button", { name: "队列（2）", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "队列（2）", exact: true });
  await expect(drawer.getByRole("listitem")).toHaveCount(2);
  await expect(drawer.getByRole("listitem").filter({ hasText: slow.track.title })).toHaveAttribute("aria-current", "true");
  await drawer.getByRole("button", { name: "关闭队列" }).click();
  await page.reload();
  await expect(bar(page).getByText(slow.track.title, { exact: true })).toBeVisible();
  await expect(bar(page).getByRole("button", { name: "播放模式：列表循环", exact: true })).toBeVisible();
  expect(await queue(context.request, device.zone_id)).toEqual(entries);
  expect((await snapshot(context.request)).devices).toHaveLength(1);
  expect(await page.evaluate((key) => sessionStorage.getItem(key), key)).toBeNull();
  expect(await sounding(page)).toEqual([]);
  await bar(page).getByRole("button", { name: "播放", exact: true }).click();
  await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0).toBeGreaterThan(1);
  expect((await zone(context.request, device.zone_id)).stream?.segments[0]?.track_start_seconds).toBe(42);
});

test("本机 ALAC 经真实渲染流播放，歌词与系统媒体信息同步，过半后一次访问只计一次", async ({ page, context, request }) => {
  test.setTimeout(65_000);
  const albums = await fixtures(request);
  const account = await member(request);
  await login(context.request, account.credentials);
  const alac = trackNamed(albums, "Zone ALAC");
  const slow = trackNamed(albums, "Slow Revocation");
  const device = await openOutput(page, "本机 ALAC 输出");
  const history = async () => decodeHistory(await body(await context.request.get("/api/v1/me/plays/tracks?page=1&page_size=100"))).items;
  await command(context.request, device.zone_id, { command: "set_settings", settings: { leveling: "off", crossfade: false, quality: "lossless" } });
  await test.step("ALAC 来源经无损流实际解码，歌词随真实时间与 Media Session 同步", async () => {
    await startFromAlbum(page, alac);
    await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0).toBeGreaterThan(0.2);
    const playing = await zone(context.request, device.zone_id);
    expect(playing.current?.track.track_id).toBe(alac.track.id);
    expect(playing.stream?.quality).toBe("lossless");
    expect(playing.stream?.format).toMatchObject({ codec: "flac", sample_rate: 44_100, bit_depth: 16, channels: 2 });
    expect(playing.signal?.steps.find((item) => item.kind === "source")?.params.codec).toBe("alac");
    expect(new URL((await sounding(page))[0]?.src ?? "").pathname).toBe(playing.stream?.url);
    await bar(page).getByRole("button", { name: "暂停", exact: true }).click();
    await expect.poll(async () => (await sounding(page)).length).toBe(0);
    await command(context.request, device.zone_id, { command: "seek", position_seconds: 0 });
    await bar(page).getByRole("button", { name: "打开全屏播放器" }).click();
    const fullscreen = page.getByRole("dialog", { name: alac.track.title, exact: true });
    const lyrics = fullscreen.getByRole("region", { name: "歌词", exact: true });
    await expect(lyrics.getByRole("button", { name: "真实渲染歌词", exact: true })).toBeVisible();
    await expect(lyrics.getByRole("listitem").filter({ hasText: "真实渲染歌词" })).toHaveAttribute("aria-current", "true");
    await fullscreen.getByRole("button", { name: "播放", exact: true }).click();
    await expect(lyrics.getByRole("listitem").filter({ hasText: "第二行歌词" })).toHaveAttribute("aria-current", "true", { timeout: 8_000 });
    await expect.poll(() => page.evaluate(() => ({ title: navigator.mediaSession.metadata?.title, album: navigator.mediaSession.metadata?.album, status: navigator.mediaSession.playbackState }))).toEqual({ title: alac.track.title, album: "Zone Album B", status: "playing" });
    // min(12/2, 240) = 6 秒；不通过 seek 或客户端上报伪造出声时长。
    await expect.poll(async () => (await history()).find((item) => item.track_id === alac.track.id)?.play_count, { timeout: 20_000 }).toBe(1);
    const current = await zone(context.request, device.zone_id);
    expect(current.current?.track.track_id).toBe(alac.track.id);
    await fullscreen.getByRole("button", { name: "收起全屏播放器" }).click();
  });
  await test.step("同一访问继续到真实曲尾，历史仍只有一条", async () => {
    await expect.poll(() => page.evaluate(() => window.playbackTestEvents.filter((event) => event.type === "ended" && /\/me\/playback\/zones\//.test(event.src)).length), { timeout: 20_000 }).toBeGreaterThan(0);
    await expect.poll(async () => (await zone(context.request, device.zone_id)).intent).toBe("pause");
    expect(await history()).toEqual([{ track_id: alac.track.id, play_count: 1 }]);
    await page.goto("/me/history");
    await expect(page.getByRole("heading", { name: "播放历史", exact: true })).toBeVisible();
    await expect(page.getByRole("table", { name: "播放历史", exact: true }).getByText(alac.track.title, { exact: true })).toBeVisible();
    await expect.poll(async () => (await snapshot(context.request)).devices.find((item) => item.device_id === device.device_id)?.online).toBe(true);
    await page.getByRole("heading", { name: "播放历史", exact: true }).click();
  });
  await test.step("只定位到长曲近末尾不会被算作听过半首", async () => {
    await command(context.request, device.zone_id, { command: "play_tracks", track_ids: [slow.track.id], start_index: 0 });
    await expect.poll(async () => (await sounding(page)).length).toBe(1);
    await command(context.request, device.zone_id, { command: "seek", position_seconds: 175 });
    await expect.poll(async () => (await zone(context.request, device.zone_id)).stream?.segments[0]?.track_start_seconds).toBe(175);
    await expect.poll(async () => (await zone(context.request, device.zone_id)).intent, { timeout: 15_000 }).toBe("pause");
    expect(await history()).toEqual([{ track_id: alac.track.id, play_count: 1 }]);
  });
});

test("本机电平化采用真实分析增益，同专辑两首通过同一条渲染流无缝衔接", async ({ page, context, request }) => {
  test.setTimeout(130_000);
  const albums = await fixtures(request);
  const account = await member(request);
  await login(context.request, account.credentials);
  const first = trackNamed(albums, "Zone First");
  const second = trackNamed(albums, "Zone Second");
  expect(first.release.id).toBe(second.release.id);
  const analysis = async () => decodeAnalysis(await body(await context.request.get(`/api/v1/tracks/${first.track.id}/analysis`)));
  // 分析工作者空闲轮询为 60 秒，六首素材逐首低优先级分析；等真实状态，不依赖其他用例耗时。
  await expect.poll(async () => (await analysis()).status, { timeout: 90_000 }).toBe("done");
  const gain = (await analysis()).analysis?.track_gain_db;
  if (gain === undefined || gain === null) throw new Error("正弦素材缺少真实分析增益，不能只断言电平化设置保存");
  expect(Math.abs(gain)).toBeGreaterThan(0.1);
  const device = await openOutput(page, "电平化与无缝输出");
  await page.goto("/settings/playback");
  const leveling = page.getByRole("group", { name: "音量均衡", exact: true }).getByRole("radio", { name: "按曲目", exact: true });
  await leveling.click();
  await expect(leveling).toBeChecked();
  await page.getByRole("switch", { name: "无缝播放", exact: true }).setChecked(true);
  await page.getByRole("switch", { name: "交叉渐变", exact: true }).setChecked(false);
  await page.getByRole("group", { name: "音质", exact: true }).getByRole("radio", { name: "无损", exact: true }).check();
  await expect.poll(async () => (await zone(context.request, device.zone_id)).settings).toMatchObject({ leveling: "track", gapless: true, crossfade: false, quality: "lossless" });
  const stream = await test.step("信号路径包含真实非零电平化增益，浏览器解码的是该渲染流", async () => {
    await startFromAlbum(page, first);
    await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0).toBeGreaterThan(0.2);
    const state = await zone(context.request, device.zone_id);
    const stream = state.stream;
    if (stream === null) throw new Error("无缝验收缺少连续渲染流");
    expect(state.current?.track.track_id).toBe(first.track.id);
    expect(stream.segments.find((item) => item.track.track_id === first.track.id)?.stream_start_seconds).toBe(0);
    expect(state.signal?.steps.find((item) => item.kind === "leveling")).toMatchObject({ params: { gain_db: gain, basis: "track" } });
    expect(state.stream?.quality).toBe("lossless");
    expect(new URL((await sounding(page))[0]?.src ?? "").pathname).toBe(state.stream?.url);
    await bar(page).getByRole("button", { name: /^信号路径：/ }).click();
    const signal = page.getByRole("dialog", { name: /^信号路径：/ });
    await expect(signal.getByText(/^电平化 /)).toBeVisible();
    await signal.getByRole("button", { name: "关闭", exact: true }).click();
    return stream;
  });
  await test.step("分段边界恰为 12 秒，跨边界不换 src、不 ended、不同时播放两个元素", async () => {
    await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0, { timeout: 20_000 }).toBeGreaterThan(13);
    expect(await sounding(page)).toHaveLength(1);
    expect(new URL((await sounding(page))[0]?.src ?? "").pathname).toBe(stream.url);
    await expect(bar(page).getByText(second.track.title, { exact: true })).toBeVisible();
    const currentStream = (await zone(context.request, device.zone_id)).stream;
    expect(currentStream?.seq).toBe(stream.seq);
    expect(currentStream?.url).toBe(stream.url);
    // 服务端会裁剪已经播完的分段；按曲目找当前段，不要求两段同时存在。
    const segment = currentStream?.segments.find((item) => item.track.track_id === second.track.id);
    expect(segment?.stream_start_seconds).toBeCloseTo(12, 3);
    expect(segment?.track_start_seconds).toBe(0);
    const events = await page.evaluate(() => window.playbackTestEvents.filter((event) => /\/me\/playback\/zones\//.test(event.src)));
    expect(events.filter((event) => event.type === "ended" || event.type === "error")).toEqual([]);
    expect(new Set(events.filter((event) => event.type === "playing").map((event) => event.src)).size).toBe(1);
    await expect.poll(() => page.evaluate(() => navigator.mediaSession.metadata?.title)).toBe(second.track.title);
  });
});

test("不同专辑 5 秒交叉渐变确实缩短流分段间隔，跨边界持续播放而不是仅保存开关", async ({ page, context, request }) => {
  test.setTimeout(60_000);
  const albums = await fixtures(request);
  const account = await member(request);
  await login(context.request, account.credentials);
  const first = trackNamed(albums, "Zone Fade First");
  const second = trackNamed(albums, "Zone Fade Second");
  expect(first.release.id).not.toBe(second.release.id);
  // 合同要求两首有效长度都大于 3×渐变时长；12 秒素材不会触发 5 秒渐变。
  const device = await openOutput(page, "跨专辑渐变输出");
  await page.goto("/settings/playback");
  const leveling = page.getByRole("group", { name: "音量均衡", exact: true }).getByRole("radio", { name: "关", exact: true });
  await leveling.click();
  await expect(leveling).toBeChecked();
  const crossfade = page.getByRole("switch", { name: "交叉渐变", exact: true });
  await crossfade.click();
  await expect(crossfade).toBeChecked();
  const duration = page.getByRole("group", { name: "渐变时长", exact: true }).getByRole("radio", { name: "5 秒", exact: true });
  await duration.click();
  await expect(duration).toBeChecked();
  await page.getByRole("switch", { name: "无缝播放", exact: true }).setChecked(true);
  await page.getByRole("group", { name: "音质", exact: true }).getByRole("radio", { name: "无损", exact: true }).check();
  await expect.poll(async () => (await zone(context.request, device.zone_id)).settings).toMatchObject({ leveling: "off", gapless: true, crossfade: true, crossfade_seconds: 5, quality: "lossless" });
  await expect.poll(async () => (await snapshot(context.request)).devices.find((item) => item.device_id === device.device_id)?.online).toBe(true);
  // 队列建立经公开协议，处理设置由真实设置页提交；不拦截渲染请求、不调用业务内部对象。
  await command(context.request, device.zone_id, { command: "play_tracks", track_ids: [first.track.id, second.track.id], start_index: 0 });
  await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0).toBeGreaterThan(0.2);
  const state = await zone(context.request, device.zone_id);
  const stream = state.stream;
  if (stream === null) throw new Error("交叉渐变没有产生渲染流");
  expect(stream.format).toMatchObject({ codec: "flac", sample_rate: 44_100, bit_depth: 16, channels: 2 });
  expect(state.current?.track.track_id).toBe(first.track.id);
  expect(stream.segments.find((item) => item.track.track_id === first.track.id)?.stream_start_seconds).toBe(0);
  expect(new URL((await sounding(page))[0]?.src ?? "").pathname).toBe(stream.url);
  await expect.poll(async () => (await sounding(page))[0]?.seconds ?? 0, { timeout: 25_000 }).toBeGreaterThan(16);
  expect(await sounding(page)).toHaveLength(1);
  expect(new URL((await sounding(page))[0]?.src ?? "").pathname).toBe(stream.url);
  await expect(bar(page).getByText(second.track.title, { exact: true })).toBeVisible();
  const currentStream = (await zone(context.request, device.zone_id)).stream;
  expect(currentStream?.seq).toBe(stream.seq);
  expect(currentStream?.url).toBe(stream.url);
  const segment = currentStream?.segments.find((item) => item.track.track_id === second.track.id);
  if (segment === undefined) throw new Error("交叉渐变缺少第二首分段");
  // 两首 20 秒：淡入从第 15 秒开始，允许分析静音边界与 8 帧对齐的微小误差，而非容忍退化为 20 秒拼接。
  expect(segment.stream_start_seconds).toBeGreaterThan(14.95);
  expect(segment.stream_start_seconds).toBeLessThan(15.05);
  expect(segment.track_start_seconds).toBeGreaterThanOrEqual(0);
  expect(segment.track_start_seconds).toBeLessThan(0.05);
  await expect.poll(() => page.evaluate(() => navigator.mediaSession.metadata?.title)).toBe(second.track.title);
  const events = await page.evaluate(() => window.playbackTestEvents.filter((event) => /\/me\/playback\/zones\//.test(event.src)));
  expect(events.filter((event) => event.type === "ended" || event.type === "error")).toEqual([]);
});
