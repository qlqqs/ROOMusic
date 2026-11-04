import { readFileSync } from "node:fs";
import type { Locator, Page, Route } from "@playwright/test";
import { albumGroup, mediaOf, releaseDetailOf } from "../src/test/catalog-data";
import { LONG_ARTIST, LONG_TITLE, LONG_TOKEN } from "./support/albums-data";
import { expectNoDocumentScroll, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import type { ApiMock } from "./support/api-mock";

// 仅路由模拟 API；音频取仓库内由编码器生成的合成立体声 FLAC，不替换生产 Audio、play 或媒体事件。
// 本组验证界面/键盘/布局，不替代 real/ 的鉴权、撤销、格式矩阵与真实服务实播验收。
const flac = readFileSync(new URL("../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));
const bar = (page: Page) => page.getByRole("region", { name: "播放器", exact: true });
const queue = (page: Page) => page.locator("#queue > ol > li");

/** 按 Range 提供合成 FLAC（直出与派生的音频地址共用）。 */
async function fulfillFlac(route: Route) {
  const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
  const start = Number(range?.[1] ?? 0);
  const end = Math.min(Number(range?.[2] || flac.length - 1), flac.length - 1);
  if (start > end) {
    await route.fulfill({ status: 416, headers: { "Content-Range": `bytes */${flac.length}` } });
    return;
  }
  await route.fulfill({ status: range ? 206 : 200, contentType: "audio/flac", body: flac.subarray(start, end + 1), headers: {
    "Accept-Ranges": "bytes", "Content-Length": String(end - start + 1),
    ...(range ? { "Content-Range": `bytes ${start}-${end}/${flac.length}` } : {}),
  } });
}

type LyricsReply = { status: number; body: unknown };

/** 歌词接口：默认每首曲目都没有歌词；专项用例传入自己的回答（后注册的路由优先）。 */
async function mockLyrics(page: Page, reply: (trackId: string) => LyricsReply = (trackId) => ({ status: 200, body: { track_id: trackId, lyrics: null } })) {
  await page.route("**/api/v1/tracks/*/lyrics", (route) => {
    const trackId = new URL(route.request().url()).pathname.split("/").at(-2) ?? "";
    const { status, body } = reply(trackId);
    return route.fulfill({ status, json: body });
  });
}

/** 只观察原生 Audio 引用；代理构造器仍返回真实 HTMLAudioElement，不改变任何行为。 */
async function observeAudio(page: Page) {
  await page.addInitScript(() => {
    const nativeAudio = window.Audio;
    const observed: HTMLAudioElement[] = [];
    Object.defineProperty(window, "playerMockObservedAudio", { value: observed });
    window.Audio = new Proxy(nativeAudio, { construct(target, args) {
      const audio = Reflect.construct(target, args) as HTMLAudioElement;
      observed.push(audio);
      return audio;
    } });
  });
}

async function installPlayback(page: Page, mock: ApiMock, long = false) {
  const group = albumGroup(91, { title: long ? LONG_TITLE : "播放专项专辑", artist: long ? LONG_ARTIST : "合成艺术家", track_count: 18 });
  const release = releaseDetailOf(group.representative, {
    media: mediaOf(group.representative, (_medium, track) => ({
      title: long ? `${track} ${track === 1 ? LONG_TITLE : LONG_TOKEN}` : `合成曲目 ${track}`,
      artist: long ? LONG_ARTIST : "合成艺术家", source_kind: "flac_vorbis_comment", codec: "flac",
      duration_seconds: 3, bit_depth: 16, sample_rate: 44100, channels: 2, cue: null,
    })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  await page.route("**/api/v1/playback/formats", (route) => route.fulfill({ json: { items: [{ source_kind: "flac_vorbis_comment", codec: "flac", delivery: "original", mime_type: "audio/flac", codecs: null }] } }));
  await page.route("**/api/v1/tracks/*/playback", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2);
    return route.fulfill({ json: { track_id: id, representation: { kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length } } });
  });
  await page.route("**/api/v1/tracks/*/audio", fulfillFlac);
  await mockLyrics(page);
  await observeAudio(page);
  await page.goto(`/albums/${group.id}`);
  await expect(page.getByRole("button", { name: "播放整张专辑", exact: true })).toBeEnabled();
  return { group, release };
}

async function media(page: Page) {
  return page.evaluate(() => {
    const observed = Reflect.get(window, "playerMockObservedAudio") as HTMLAudioElement[];
    const audio = observed.at(-1);
    return audio ? { paused: audio.paused, time: audio.currentTime, src: audio.currentSrc, sourceAttribute: audio.getAttribute("src"), duration: audio.duration, error: audio.error?.code ?? null } : null;
  });
}

async function keyboardActivate(page: Page, target: Locator) {
  // 用 Tab 而不是 click/focus 抵达控件，验证它确实在键盘顺序中且具有可见焦点。
  for (let index = 0; index < 250; index += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) {
      await expectVisibleFocusRing(page, "播放器键盘操作");
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error("键盘无法抵达目标控件");
}

async function startAndPause(page: Page) {
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await expect(bar(page).getByRole("button", { name: "暂停", exact: true })).toBeVisible();
  await bar(page).getByRole("button", { name: "暂停", exact: true }).click();
  await expect.poll(async () => (await media(page))?.paused).toBe(true);
  expect((await media(page))?.error).toBeNull();
}

async function expectLastItemUncovered(page: Page, last: Locator, label: string) {
  await last.evaluate((element) => element.scrollIntoView({ block: "end" }));
  // 固定底栏不会影响scrollIntoView的可见区；必须滚到容器末尾，才能核对实际预留空间。
  await page.locator("#main").evaluate((main) => main.scrollTo(0, main.scrollHeight));
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  const item = await last.boundingBox();
  const player = await bar(page).boundingBox();
  expect(item, `${label}：最后项已渲染`).not.toBeNull();
  expect(player, `${label}：底栏已渲染`).not.toBeNull();
  // 长标题单项可高于视口；验证它的尾部与操作仍能滚到播放栏上方，而不是强求整项同时可见。
  expect((item?.y ?? 0) + (item?.height ?? 0)).toBeGreaterThan(0);
  expect((item?.y ?? 0) + (item?.height ?? 0), `${label}：底栏不能遮住最后项`).toBeLessThanOrEqual((player?.y ?? 0) + 1);
}

test("空队列隐藏底栏且不遗留占位或伪造曲目信息", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await expect(bar(page)).toHaveCount(0);
  await expect(page.locator("[data-player]")).toHaveCount(0);
  expect(await page.locator("#main").evaluate((main) => getComputedStyle(main).paddingBottom)).toBe("0px");
  expect(await media(page)).toMatchObject({ paused: true, src: "" });
  await page.goto("/now-playing");
  await expect(page.getByText("队列是空的。去专辑选择一张唱片，开始聆听。")).toBeVisible();
  await expectNoSeriousViolations(page, "空播放器");
});

for (const width of widths) {
  test(`${width}px：空/有队列、长标题专辑与正在播放不溢出，底栏不遮最后项`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    await installPlayback(page, mock, true);
    const tracks = page.locator("#main ol > li");
    await expect(tracks).toHaveCount(18);
    await expectNoHorizontalOverflow(page, "长标题空播放器");
    await expect(bar(page)).toHaveCount(0);
    await expect(page.locator("[data-player]")).toHaveCount(0);
    expect(await page.locator("#main").evaluate((main) => getComputedStyle(main).paddingBottom)).toBe("0px");
    await tracks.last().scrollIntoViewIfNeeded();
    await expect(tracks.last()).toBeInViewport();
    await startAndPause(page);
    await expect(bar(page)).toContainText(LONG_TITLE);
    await expectNoHorizontalOverflow(page, "长标题专辑播放器");
    await expectNoDocumentScroll(page, "专辑播放器");
    await expectLastItemUncovered(page, tracks.last(), "有队列专辑");
    await expectNoSeriousViolations(page, "专辑播放动作");
    await bar(page).getByRole("link", { name: "队列（18）", exact: true }).click();
    await expect(queue(page)).toHaveCount(18);
    await page.locator("#main").evaluate((main) => main.scrollTo(0, 0));
    await page.evaluate(() => window.scrollTo(0, 0));
    const heroTitle = page.getByRole("heading", { level: 2, name: `1 ${LONG_TITLE}`, exact: true });
    const artist = page.locator("#main").getByText(LONG_ARTIST, { exact: true });
    const album = page.locator("#main").getByRole("link", { name: LONG_TITLE, exact: true });
    await expect(heroTitle).toHaveAttribute("title", `1 ${LONG_TITLE}`);
    await expect(artist).toHaveAttribute("title", LONG_ARTIST);
    await expect(album).toHaveAttribute("title", LONG_TITLE);
    expect(await heroTitle.evaluate((element) => getComputedStyle(element).webkitLineClamp)).toBe("4");
    expect(await artist.evaluate((element) => getComputedStyle(element).webkitLineClamp)).toBe("2");
    expect(await album.evaluate((element) => element.parentElement === null ? "" : getComputedStyle(element.parentElement).webkitLineClamp)).toBe("3");
    const info = heroTitle.locator("..");
    expect((await info.boundingBox())?.height).toBeLessThanOrEqual(650);
    if (width >= 768) {
      // 首屏是“封面 | 歌词”，标题与控制在两栏下方：长标题时它们可能在首屏之外，但必须能滚动到达；底栏的播放按钮始终在首屏内。
      await expect(bar(page).getByRole("button", { name: /^(播放|暂停)$/ })).toBeInViewport();
      await info.getByRole("button", { name: "播放", exact: true }).scrollIntoViewIfNeeded();
      await expect(info.getByRole("button", { name: "播放", exact: true })).toBeInViewport();
    }
    expect((await queue(page).last().boundingBox())?.height).toBeLessThanOrEqual(300);

    await expectNoHorizontalOverflow(page, "正在播放长标题");
    await expectNoDocumentScroll(page, "正在播放");
    await expectLastItemUncovered(page, queue(page).last(), "正在播放队列");
    await expectNoSeriousViolations(page, "正在播放与队列动作");
    if (process.env["ROOMUSIC_PLAYER_SCREENSHOTS"] === "1" && (width === 375 || width === 1280)) {
      await page.locator("#main").evaluate((main) => main.scrollTo(0, 0));
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ path: `/tmp/roomusic-player-${width}.png` });
    }
  });
}

type DerivedOptions = {
  /** 前几次预检返回 202（每次挂起 holdMs，像真实服务端的长轮询）；之后返回派生表示。Infinity 表示一直转换。 */
  preparingPolls: number;
  holdMs: number;
  /** 对指定曲目序号（从 0 起）的预检直接返回这个错误。 */
  failing?: { index: number; status: number; code: string };
};

/** ALAC 专辑：格式表带派生项；预检按 DerivedOptions 返回 202、派生表示或错误；音频仍是合成 FLAC（与服务端转换后的 audio/flac 相同）。 */
async function installDerivedPlayback(page: Page, mock: ApiMock, options: DerivedOptions) {
  const group = albumGroup(92, { title: "无损转换专辑", artist: "合成艺术家", track_count: 2 });
  const release = releaseDetailOf(group.representative, {
    media: mediaOf(group.representative, (_medium, track) => ({
      title: `ALAC 曲目 ${track}`, artist: "合成艺术家", source_kind: "m4a", codec: "alac",
      duration_seconds: 3, bit_depth: 16, sample_rate: 44100, channels: 2, cue: null,
    })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  const tracks = release.media[0]?.tracks ?? [];
  const polls = new Map<string, number>();
  const behaviour = { ...options };
  await page.route("**/api/v1/playback/formats", (route) => route.fulfill({ json: { items: [
    { source_kind: "flac_vorbis_comment", codec: "flac", delivery: "original", mime_type: "audio/flac", codecs: null },
    { source_kind: "m4a", codec: "alac", delivery: "derived", mime_type: "audio/flac", codecs: null },
  ] } }));
  await page.route("**/api/v1/tracks/*/playback", async (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2) ?? "";
    const count = (polls.get(id) ?? 0) + 1;
    polls.set(id, count);
    const failing = behaviour.failing;
    if (failing !== undefined && tracks[failing.index]?.id === id) {
      await route.fulfill({ status: failing.status, json: { error: { code: failing.code, message: "后端文案" }, request_id: "mock" } }).catch(() => undefined);
      return;
    }
    if (count <= behaviour.preparingPolls) {
      await new Promise((resolve) => setTimeout(resolve, behaviour.holdMs));
      await route.fulfill({ status: 202, json: { track_id: id, status: "preparing" } }).catch(() => undefined);
      return;
    }
    await route.fulfill({ json: { track_id: id, representation: { kind: "derived", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length } } }).catch(() => undefined);
  });
  await page.route("**/api/v1/tracks/*/audio", fulfillFlac);
  await mockLyrics(page);
  await observeAudio(page);
  await page.goto(`/albums/${group.id}`);
  await expect(page.getByRole("button", { name: "播放整张专辑", exact: true })).toBeEnabled();
  return { group, release, tracks, polls, behaviour };
}

test("ALAC 曲目经服务端转换后播放：先显示“正在准备”，转换完成自动出声，信号路径写明无损转换，并预取下一首", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { tracks, polls } = await installDerivedPlayback(page, mock, { preparingPolls: 2, holdMs: 600 });
  const [first, second] = tracks;
  if (first === undefined || second === undefined) throw new Error("专项素材缺少两首曲目");
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect(bar(page).getByText("正在准备", { exact: true })).toBeVisible();
  // 准备中也是“要播放”的状态：按钮写“暂停”，音频元素还没有来源。
  await expect(bar(page).getByRole("button", { name: "暂停", exact: true })).toBeVisible();
  expect(await media(page)).toMatchObject({ paused: true, src: "" });
  await expect.poll(async () => (await media(page))?.paused, { timeout: 15_000 }).toBe(false);
  expect((await media(page))?.src).toContain(first.id);
  expect((await media(page))?.error).toBeNull();
  await expect(bar(page).getByText("正在播放", { exact: true })).toBeVisible();
  expect(polls.get(first.id)).toBe(3);
  // 第一首出声之后，为下一首派生曲目预检一次（它收到 202，由第二首自己的预检继续等待）。
  await expect.poll(() => polls.get(second.id)).toBe(1);
  await bar(page).getByRole("link", { name: "队列（2）", exact: true }).click();
  const server = page.getByRole("heading", { name: "服务端", exact: true }).locator("..");
  await expect(server).toContainText("无损转换");
  await expect(server).toContainText("重新编码为 FLAC");
  await expect(server).toContainText("PCM 一致");
  await expect(server).not.toContainText("原文件直出");
  await expectNoSeriousViolations(page, "ALAC 正在播放与信号路径");
});

test("等待转换时暂停：放弃等待并不再发出预检", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { tracks, polls } = await installDerivedPlayback(page, mock, { preparingPolls: Number.POSITIVE_INFINITY, holdMs: 300 });
  const first = tracks[0];
  if (first === undefined) throw new Error("专项素材缺少曲目");
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect(bar(page).getByText("正在准备", { exact: true })).toBeVisible();
  await bar(page).getByRole("button", { name: "暂停", exact: true }).click();
  await expect(bar(page).getByText("已暂停", { exact: true })).toBeVisible();
  await expect(bar(page).getByRole("button", { name: "播放", exact: true })).toBeVisible();
  const afterPause = polls.get(first.id) ?? 0;
  await page.waitForTimeout(1500);
  expect(polls.get(first.id)).toBe(afterPause);
  expect(await media(page)).toMatchObject({ paused: true, src: "" });
});

test("服务器忙于转换：提示可重试，重试后正常播放；转换失败的曲目写明原因并前进到下一首", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { tracks, behaviour } = await installDerivedPlayback(page, mock, { preparingPolls: 0, holdMs: 0, failing: { index: 0, status: 503, code: "derivation_busy" } });
  const [first, second] = tracks;
  if (first === undefined || second === undefined) throw new Error("专项素材缺少两首曲目");
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect(bar(page).getByText("服务器正忙于转换，进入正在播放页重试", { exact: true })).toBeVisible();
  await bar(page).getByRole("link", { name: "ALAC 曲目 1", exact: true }).click();
  await expect(page.getByText("服务器正忙于转换，请稍后重试。", { exact: true })).toBeVisible();
  await expectNoSeriousViolations(page, "服务器忙");
  delete behaviour.failing;
  await page.getByRole("button", { name: "重试播放", exact: true }).click();
  await expect.poll(async () => (await media(page))?.paused, { timeout: 15_000 }).toBe(false);
  expect((await media(page))?.src).toContain(first.id);
  await expect(page.getByRole("button", { name: "重试播放", exact: true })).toHaveCount(0);

  behaviour.failing = { index: 1, status: 409, code: "derivation_failed" };
  await page.getByRole("button", { name: "下一首", exact: true }).first().click();
  await expect(queue(page).nth(1)).toContainText("无损转换失败，无法播放这首曲目");
  await expectNoSeriousViolations(page, "转换失败");
  expect(second.id).not.toBe(first.id);
});

for (const width of widths) {
  test(`${width}px：ALAC 正在准备与无损转换的信号路径不溢出，axe 无严重问题`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    await installDerivedPlayback(page, mock, { preparingPolls: Number.POSITIVE_INFINITY, holdMs: 500 });
    await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
    await expect(bar(page).getByRole("button", { name: "暂停", exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page, "正在准备的专辑页");
    await expectNoSeriousViolations(page, "正在准备的专辑页");
    await bar(page).getByRole("link", { name: "队列（2）", exact: true }).click();
    await expect(page.getByText(/^正在准备 · 第 1 首 \/ 共 2 首$/)).toBeVisible();
    const server = page.getByRole("heading", { name: "服务端", exact: true }).locator("..");
    await expect(server).toContainText("无损转换");
    await expectNoHorizontalOverflow(page, "正在准备的正在播放页");
    await expectNoDocumentScroll(page, "正在准备的正在播放页");
    await expectNoSeriousViolations(page, "正在准备的正在播放页");
  });
}

test("纯键盘：播放、下一首播放、不打断当前、切歌、队列上移/移除和清空确认", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { release } = await installPlayback(page, mock);
  const third = release.media[0]?.tracks[2];
  if (!third) throw new Error("专项素材缺少第三首");
  await keyboardActivate(page, page.getByRole("button", { name: "播放整张专辑", exact: true }));
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await keyboardActivate(page, bar(page).getByRole("button", { name: "暂停", exact: true }));
  const firstSrc = (await media(page))?.src;
  await keyboardActivate(page, page.locator("#main ol > li").nth(2).getByRole("button", { name: "更多", exact: true }));
  await page.getByRole("menuitem", { name: "下一首播放", exact: true }).press("Enter");
  await expect(bar(page).getByRole("link", { name: "队列（19）", exact: true })).toBeVisible();
  expect(await media(page)).toMatchObject({ src: firstSrc, paused: true });
  await keyboardActivate(page, bar(page).getByRole("button", { name: "下一首", exact: true }));
  await expect.poll(async () => (await media(page))?.src).toContain(third.id);
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await keyboardActivate(page, bar(page).getByRole("button", { name: "暂停", exact: true }));
  await keyboardActivate(page, bar(page).getByRole("link", { name: "队列（19）", exact: true }));
  await expect(queue(page)).toHaveCount(19);
  await expect(queue(page).nth(1)).toContainText(third.title);
  await keyboardActivate(page, queue(page).nth(1).getByRole("button", { name: "上移", exact: true }));
  await expect(queue(page).first()).toContainText(third.title);
  await expect(queue(page).first()).toHaveAttribute("aria-current", "true");
  await keyboardActivate(page, queue(page).first().getByRole("button", { name: "移除", exact: true }));
  await expect(queue(page)).toHaveCount(18);
  await expect(queue(page).first().getByRole("button", { name: "移除", exact: true })).toBeFocused();
  await keyboardActivate(page, page.getByRole("button", { name: "清空队列", exact: true }));
  const dialog = page.getByRole("dialog", { name: "清空播放队列？", exact: true });
  await expect(dialog).toBeVisible();
  await expectNoSeriousViolations(page, "清空确认");
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(queue(page)).toHaveCount(18);
  await keyboardActivate(page, page.getByRole("button", { name: "清空队列", exact: true }));
  await keyboardActivate(page, dialog.getByRole("button", { name: "清空队列", exact: true }));
  await expect(bar(page)).toHaveCount(0);
  await expect(page.locator("[data-player]")).toHaveCount(0);
  expect(await page.locator("#main").evaluate((main) => getComputedStyle(main).paddingBottom)).toBe("0px");
  await expect(page.getByText("队列是空的。去专辑选择一张唱片，开始聆听。")).toBeVisible();
  await expect.poll(() => media(page)).toMatchObject({ paused: true, sourceAttribute: null });
});

// ---- 歌词 ----

const LYRIC_LINES = 30;

/** 同步歌词：每 100 毫秒一行（合成 FLAC 约 3 秒，全部行都能到达）；有一行同时间的对照译文与一个间奏。 */
function syncedLyrics(trackId: string, count = LYRIC_LINES) {
  const lines = Array.from({ length: count }, (_, index) => ({ start_ms: index * 100, text: `第 ${index + 1} 行歌词${"词".repeat(index % 5)}` }));
  return { track_id: trackId, lyrics: { kind: "synced", source: "embedded", lines } };
}
function plainLyrics(trackId: string) {
  const lines = Array.from({ length: 24 }, (_, index) => ({ start_ms: null, text: index % 4 === 3 ? "" : `纯文本第 ${index + 1} 行${"词".repeat(index % 6)}` }));
  return { track_id: trackId, lyrics: { kind: "plain", source: "embedded", lines } };
}
const lyricsReplies = {
  synced: (trackId: string): LyricsReply => ({ status: 200, body: syncedLyrics(trackId) }),
  plain: (trackId: string): LyricsReply => ({ status: 200, body: plainLyrics(trackId) }),
  none: (trackId: string): LyricsReply => ({ status: 200, body: { track_id: trackId, lyrics: null } }),
};

/** 移到正在播放页：专辑页开始播放、暂停，再从底栏进入正在播放。 */
async function openNowPlaying(page: Page) {
  await startAndPause(page);
  await bar(page).getByRole("link", { name: "队列（18）", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "正在播放", exact: true })).toBeVisible();
}

const lyricsPanel = (page: Page) => page.locator("#main").getByRole("region", { name: "歌词", exact: true });

async function setMediaTime(page: Page, seconds: number) {
  await page.evaluate((time) => {
    const observed = Reflect.get(window, "playerMockObservedAudio") as HTMLAudioElement[];
    const audio = observed.at(-1);
    if (audio) audio.currentTime = time;
  }, seconds);
}

/** 容器宽于 760px 是“封面 | 歌词”两栏、标题与控制在两栏下方；窄于 760px 是封面、标题与控制、歌词依次排列。 */
async function expectLyricsLayout(page: Page, label: string) {
  const main = page.locator("#main");
  const lyrics = lyricsPanel(page);
  const cover = main.getByLabel("没有可用封面").locator("..");
  const title = main.getByRole("heading", { level: 2, name: "合成曲目 1", exact: true });
  const [lyricsBox, coverBox, titleBox, container] = await Promise.all([lyrics.boundingBox(), cover.boundingBox(), title.boundingBox(), page.locator("#queue").evaluate((element) => element.clientWidth)]);
  if (lyricsBox === null || coverBox === null || titleBox === null) throw new Error(`${label}：缺少布局元素`);
  const viewport = page.viewportSize();
  if (container >= 760) {
    expect(lyricsBox.x, `${label}：歌词在封面右侧`).toBeGreaterThanOrEqual(coverBox.x + coverBox.width - 1);
    expect(titleBox.y, `${label}：标题与控制在两栏下方`).toBeGreaterThanOrEqual(Math.max(lyricsBox.y + lyricsBox.height, coverBox.y + coverBox.height) - 1);
    expect(Math.abs(lyricsBox.height - Math.max(coverBox.height, 280)), `${label}：歌词与封面同高`).toBeLessThanOrEqual(2);
  } else {
    expect(titleBox.y, `${label}：标题与控制在封面下方`).toBeGreaterThanOrEqual(coverBox.y + coverBox.height - 1);
    expect(lyricsBox.y, `${label}：歌词在标题与控制之后`).toBeGreaterThanOrEqual(titleBox.y + titleBox.height - 1);
    expect(coverBox.width, `${label}：单栏封面最大 280px`).toBeLessThanOrEqual(281);
    expect(lyricsBox.height, `${label}：歌词最高 60vh`).toBeLessThanOrEqual((viewport?.height ?? 900) * 0.6 + 1);
  }
}

for (const width of widths) {
  for (const kind of ["synced", "plain", "none"] as const) {
    test(`${width}px：${{ synced: "同步", plain: "纯文本", none: "没有" }[kind]}歌词的正在播放页布局不溢出，axe 无严重问题`, async ({ page, backend }) => {
      await page.setViewportSize({ width, height: 900 });
      const mock = await backend.start({ signedInAs: "listener" });
      await installPlayback(page, mock);
      await mockLyrics(page, lyricsReplies[kind]);
      await openNowPlaying(page);
      const lyrics = lyricsPanel(page);
      await expect(lyrics).toBeVisible();
      if (kind === "synced") await expect(lyrics.getByRole("listitem")).toHaveCount(LYRIC_LINES);
      if (kind === "plain") await expect(lyrics.getByText("内嵌歌词 · 纯文本", { exact: true })).toBeVisible();
      if (kind === "none") await expect(lyrics.getByText("这首曲目没有内嵌歌词", { exact: true })).toBeVisible();
      await expectLyricsLayout(page, `${width}px ${kind}`);
      await expectNoHorizontalOverflow(page, `${kind}歌词`);
      await expectNoDocumentScroll(page, `${kind}歌词`);
      await expectNoSeriousViolations(page, `${kind}歌词`);
      if (process.env["ROOMUSIC_LYRICS_SCREENSHOTS"] === "1" && (width === 375 || width === 768 || width === 1280)) {
        // 供人工确认布局（截图写到 /tmp，不入库）：同步歌词先把播放位置移到中段，让高亮停在列表中部；
        // 每种状态两张：页面顶部，以及把歌词面板滚到视野中央。
        if (kind === "synced") {
          await setMediaTime(page, 1.55);
          await expect(lyrics.locator("[aria-current='true']")).toContainText("第 16 行歌词");
          await page.waitForTimeout(800);
        }
        for (const [name, target] of [["top", null], ["lyrics", lyrics]] as const) {
          await page.locator("#main").evaluate((main) => main.scrollTo(0, 0));
          await page.evaluate(() => window.scrollTo(0, 0));
          if (target !== null) await target.evaluate((element) => element.scrollIntoView({ block: "center" }));
          await page.screenshot({ path: `/tmp/roomusic-lyrics-${kind}-${width}-${name}.png` });
        }
      }
    });
  }
}

test("同步歌词：当前行随播放位置变化并保持在中部，点击与键盘跳转，高亮不改变行高", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await mockLyrics(page, lyricsReplies.synced);
  await openNowPlaying(page);
  const lyrics = lyricsPanel(page);
  const scroller = lyrics.getByRole("group", { name: "逐行歌词", exact: true });
  const items = lyrics.getByRole("listitem");
  const current = lyrics.locator("[aria-current='true']");
  await expect(items).toHaveCount(LYRIC_LINES);
  // openNowPlaying 在真实播放后才暂停，位置可能已越过第 1 行的窗口（100 ms 减 40 ms 提前容差），先归零再断言。
  await setMediaTime(page, 0);
  await expect(lyrics.getByText("内嵌歌词 · 逐行同步 · 点任一行跳到该处", { exact: true })).toBeVisible();
  await expect(current).toContainText("第 1 行歌词");

  // 平滑滚动需要时间：用轮询等它到位（轮询函数里不能放会抛错的断言）。
  const inside = async (item: Locator) => {
    const [box, area] = await Promise.all([item.boundingBox(), scroller.boundingBox()]);
    return box !== null && area !== null && box.y + 1 >= area.y && box.y + box.height - 1 <= area.y + area.height;
  };
  const metrics = (item: Locator) => item.evaluate((element) => {
    const style = getComputedStyle(element);
    return { fontSize: style.fontSize, fontWeight: style.fontWeight, lineHeight: style.lineHeight };
  });

  // 位置到 1.55 秒：当前行变为第 16 行（1.5 秒），并被滚到可视区内；高亮前后字号、字重、行高与整份歌词的高度都不变。
  const listHeight = async () => (await lyrics.getByRole("list").boundingBox())?.height;
  const heightBefore = await listHeight();
  const plainMetrics = await metrics(items.nth(3));
  await setMediaTime(page, 1.55);
  await expect(current).toContainText("第 16 行歌词");
  await expect(current).toHaveCount(1);
  await expect.poll(() => inside(current), { message: "当前行被滚进歌词区域" }).toBe(true);
  expect(await metrics(current)).toEqual(plainMetrics);
  expect(await listHeight()).toBe(heightBefore);
  expect(await scroller.evaluate((element) => element.scrollTop), "已滚动到当前行附近").toBeGreaterThan(0);

  // 点击第 25 行（2.4 秒）：播放位置不早于该行时间，当前行随之变化。
  await lyrics.getByRole("button", { name: /^第 25 行歌词/ }).click();
  await expect(current).toContainText("第 25 行歌词");
  expect((await media(page))?.time ?? 0).toBeGreaterThanOrEqual(2.39);

  // 键盘：焦点在当前行按钮上，方向键上移两行，Enter 跳转到第 23 行（2.2 秒）；焦点环可见。
  await lyrics.getByRole("button", { name: /^第 25 行歌词/ }).focus();
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");
  await expect(lyrics.getByRole("button", { name: /^第 23 行歌词/ })).toBeFocused();
  await expectVisibleFocusRing(page, "歌词行键盘操作");
  await page.keyboard.press("Enter");
  await expect(current).toContainText("第 23 行歌词");
  expect((await media(page))?.time ?? 0).toBeGreaterThanOrEqual(2.19);
  expect((await media(page))?.time ?? 0).toBeLessThan(2.3);
  await expectNoSeriousViolations(page, "同步歌词的当前行与焦点");
});

test("同步歌词：手动滚动后暂停跟随并出现“回到当前行”，点击后恢复并定位", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await mockLyrics(page, lyricsReplies.synced);
  await openNowPlaying(page);
  const lyrics = lyricsPanel(page);
  const scroller = lyrics.getByRole("group", { name: "逐行歌词", exact: true });
  const current = lyrics.locator("[aria-current='true']");
  const back = lyrics.getByRole("button", { name: "回到当前行", exact: true });
  const scrollTop = () => scroller.evaluate((element) => element.scrollTop);
  await expect(lyrics.getByRole("listitem")).toHaveCount(LYRIC_LINES);
  await setMediaTime(page, 2.05);
  await expect(current).toContainText("第 21 行歌词");
  await expect.poll(scrollTop).toBeGreaterThan(0);
  await expect(back).toHaveCount(0);

  // 滚轮是用户在自己浏览：暂停跟随。之后当前行变化时，滚动位置不再被拖回。
  await scroller.hover();
  await page.mouse.wheel(0, -700);
  await expect(back).toBeVisible();
  await expect.poll(scrollTop).toBeLessThan(200);
  const parked = await scrollTop();
  await setMediaTime(page, 2.75);
  await expect(current).toContainText("第 28 行歌词");
  await page.waitForTimeout(600);
  expect(Math.abs((await scrollTop()) - parked)).toBeLessThanOrEqual(2);
  await expectNoSeriousViolations(page, "暂停跟随");

  // “回到当前行”：恢复跟随并立即定位（可视区内），按钮消失。
  await back.click();
  await expect(back).toHaveCount(0);
  await expect.poll(async () => {
    const [box, area] = await Promise.all([current.boundingBox(), scroller.boundingBox()]);
    return box !== null && area !== null && box.y >= area.y && box.y + box.height <= area.y + area.height + 1;
  }).toBe(true);
});

test("歌词读取失败：显示带重试的错误，重试后显示歌词；曲目没有歌词与纯文本如实显示", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  let failing = true;
  await mockLyrics(page, (trackId) => failing
    ? { status: 503, body: { error: { code: "database_unavailable", message: "后端文案" }, request_id: "mock" } }
    : lyricsReplies.synced(trackId));
  await openNowPlaying(page);
  const lyrics = lyricsPanel(page);
  // 查询对 503 自动重试两次，之后才显示错误。
  await expect(lyrics.getByRole("alert")).toContainText("服务暂时不可用", { timeout: 20_000 });
  await expect(lyrics.getByText("这首曲目没有内嵌歌词")).toHaveCount(0);
  await expectNoSeriousViolations(page, "歌词读取失败");
  failing = false;
  await lyrics.getByRole("button", { name: "重试", exact: true }).click();
  await expect(lyrics.getByRole("listitem")).toHaveCount(LYRIC_LINES);
  await expect(lyrics.getByRole("alert")).toHaveCount(0);
});
