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
    if (width >= 768) await expect(info.getByRole("button", { name: "播放", exact: true })).toBeInViewport();
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
