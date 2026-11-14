import { readFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
import { albumGroup, mediaOf, releaseDetailOf } from "../src/test/catalog-data";
import { sampleBody } from "../src/test/api-samples";
import { LONG_ARTIST, LONG_TITLE, LONG_TOKEN } from "./support/albums-data";
import { expectNoDocumentScroll, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { installZoneAudio } from "./support/zones";
import type { ApiMock } from "./support/api-mock";

// 仅路由模拟 API；音频取仓库内由编码器生成的合成立体声 FLAC，不替换生产 Audio、play 或媒体事件。
// 本组验证界面/键盘/布局，不替代 real/ 的鉴权、撤销、格式矩阵与真实服务实播验收。
const flac = readFileSync(new URL("../../backend/testdata/playback/flac-16-44100.flac", import.meta.url));
const bar = (page: Page) => page.getByRole("region", { name: "播放器", exact: true });
const queueDialog = (page: Page) => page.getByRole("dialog", { name: /队列（\d+）/ });
const queue = (page: Page) => queueDialog(page).getByRole("list").getByRole("listitem");
const fullscreenDialog = (page: Page) => page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "收起全屏播放器", exact: true }) });

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

async function installPlayback(page: Page, mock: ApiMock, long = false, audio = { body: flac, durationSeconds: 3 }) {
  const group = albumGroup(91, { title: long ? LONG_TITLE : "播放专项专辑", artist: long ? LONG_ARTIST : "合成艺术家", track_count: 18 });
  const release = releaseDetailOf(group.representative, {
    media: mediaOf(group.representative, (_medium, track) => ({
      title: long ? `${track} ${track === 1 ? LONG_TITLE : LONG_TOKEN}` : `合成曲目 ${track}`,
      artist: long ? LONG_ARTIST : "合成艺术家", source_kind: "flac_vorbis_comment", codec: "flac",
      duration_seconds: audio.durationSeconds, bit_depth: 16, sample_rate: 44100, channels: 2, cue: null,
    })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  await installZoneAudio(page, audio.body);
  await mockLyrics(page);
  await observeAudio(page);
  await page.goto(`/albums/${group.id}`);
  await expect(page.getByRole("button", { name: "播放整张专辑", exact: true })).toBeEnabled();
  return { group, release };
}

async function media(page: Page) {
  return page.evaluate(() => {
    const observed = Reflect.get(window, "playerMockObservedAudio") as HTMLAudioElement[];
    const audio = observed.find((item) => item.getAttribute("src")?.includes("/streams/")) ?? observed.at(-1);
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

async function expectLastItemUncovered(page: Page, last: Locator, label: string) {
  await last.evaluate((element) => element.scrollIntoView({ block: "end" }));
  await page.locator("#main").evaluate((main) => main.scrollTo(0, main.scrollHeight));
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  const item = await last.boundingBox();
  const player = await bar(page).boundingBox();
  expect(item, `${label}：最后项已渲染`).not.toBeNull();
  expect(player, `${label}：底栏已渲染`).not.toBeNull();
  expect((item?.y ?? 0) + (item?.height ?? 0)).toBeGreaterThan(0);
  expect((item?.y ?? 0) + (item?.height ?? 0), `${label}：底栏不能遮住最后项`).toBeLessThanOrEqual((player?.y ?? 0) + 1);
}

async function startAndPause(page: Page) {
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await expect(bar(page).getByRole("button", { name: "暂停", exact: true })).toBeVisible();
  await bar(page).getByRole("button", { name: "暂停", exact: true }).click();
  await expect.poll(async () => (await media(page))?.paused).toBe(true);
  expect((await media(page))?.error).toBeNull();
}


test("空队列隐藏底栏且不遗留占位或伪造曲目信息", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await expect(bar(page)).toHaveCount(0);
  await expect(page.locator("[data-player]")).toHaveCount(0);
  expect(await page.locator("#main").evaluate((main) => getComputedStyle(main).paddingBottom)).toBe("0px");
  expect(await media(page)).toMatchObject({ paused: true, src: "" });
  await page.goto("/now-playing");
  await expect(page.getByRole("heading", { name: "概览", exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await expectNoSeriousViolations(page, "空播放器");
});

for (const width of widths) {
  test(`${width}px：全屏播放器、长标题与队列抽屉不溢出，axe 无严重问题`, async ({ page, backend }) => {
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
    await expectNoHorizontalOverflow(page, "专辑播放栏");
    await expectNoDocumentScroll(page, "专辑播放栏");
    await expectLastItemUncovered(page, tracks.last(), "有队列专辑");
    await expectNoSeriousViolations(page, "专辑播放动作");

    await bar(page).getByRole("button", { name: "打开全屏播放器", exact: true }).click();
    const full = fullscreenDialog(page);
    await expect(full).toBeVisible();
    // 可见时 grow 仍可能在淡入、缩放；等待结束再测布局与对比度，避免 axe 读到不同动画帧的混合颜色。
    await expect(full.locator(":scope > div").first()).toHaveCSS("transform", "none");
    await expect(full.getByRole("heading", { level: 2, name: `1 ${LONG_TITLE}`, exact: true })).toHaveAttribute("title", `1 ${LONG_TITLE}`);
    await expect(full).toContainText(LONG_ARTIST);
    const album = full.getByRole("link", { name: LONG_TITLE, exact: true });
    await expect(album).toHaveAttribute("title", LONG_TITLE);
    await expectNoHorizontalOverflow(page, "全屏长标题");
    await expectNoDocumentScroll(page, "全屏长标题");
    await expectNoSeriousViolations(page, "全屏播放器");

    const queueButton = full.getByRole("button", { name: "队列（18）", exact: true });
    await queueButton.click();
    await expect(queue(page)).toHaveCount(18);
    await expectNoHorizontalOverflow(page, "队列抽屉");
    await expectNoSeriousViolations(page, "队列抽屉");
    await page.keyboard.press("Escape");
    await expect(queueDialog(page)).not.toBeVisible();
    await expect(queueButton).toBeFocused();
    if (process.env["ROOMUSIC_PLAYER_SCREENSHOTS"] === "1" && (width === 375 || width === 1280)) {
      await page.screenshot({ path: `/tmp/roomusic-player-${width}.png` });
    }
  });
}

test("全屏播放器：Esc、收起按钮与浏览器返回都关闭，关闭后焦点返回封面", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await startAndPause(page);
  const cover = bar(page).getByRole("button", { name: "打开全屏播放器", exact: true });
  await cover.click();
  await expect(fullscreenDialog(page)).toBeVisible();
  await expect(fullscreenDialog(page).getByRole("button", { name: "收起全屏播放器" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(fullscreenDialog(page)).not.toBeVisible();
  await expect(cover).toBeFocused();

  await cover.click();
  await expect(fullscreenDialog(page)).toBeVisible();
  await fullscreenDialog(page).getByRole("button", { name: "收起全屏播放器" }).click();
  await expect(fullscreenDialog(page)).not.toBeVisible();
  await expect(cover).toBeFocused();

  await cover.click();
  await expect(fullscreenDialog(page)).toBeVisible();
  await page.goBack();
  await expect(fullscreenDialog(page)).not.toBeVisible();
  await expect(page).toHaveURL(/\/albums\//);
});

test("不可播放曲目的心形与更多仍可用，仅播放、下一首播放与加入队列禁用", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const group = albumGroup(92, { title: "不可播放曲目专项", track_count: 1 });
  const release = releaseDetailOf(group.representative, {
    // 扫描器能识别 WavPack DSD，但服务端渲染格式表尚不支持；CUE 分轨已经可播放。
    media: mediaOf(group.representative, () => ({ title: "WavPack DSD 曲目", source_kind: "wavpack_apev2", codec: "wavpack_dsd" })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  await page.goto(`/albums/${group.id}`);
  const track = page.locator("#main ol > li").filter({ has: page.getByText("WavPack DSD 曲目", { exact: true }) });
  const reason = "这种格式目前不能播放";
  const play = track.getByRole("button", { name: "从这首开始播放", exact: true });
  await expect(play).toBeDisabled();
  await expect(play).toHaveAccessibleDescription(reason);
  await expect(track.getByText(reason, { exact: true })).toBeVisible();
  await expect(track.getByRole("button", { name: "收藏《WavPack DSD 曲目》", exact: true })).toBeEnabled();
  const more = track.getByRole("button", { name: "更多操作", exact: true });
  await expect(more).toBeEnabled();
  await more.click();
  await expect(page.getByRole("menuitem", { name: "下一首播放", exact: true })).toBeDisabled();
  await expect(page.getByRole("menuitem", { name: "加入队列", exact: true })).toBeDisabled();
  await expect(page.getByRole("menuitem", { name: "添加到歌单…", exact: true })).toBeEnabled();
  await page.keyboard.press("Escape");
  await expect(more).toBeFocused();
  await expect(bar(page)).toHaveCount(0);
});

test("纯键盘：播放、下一首播放、不打断当前、切歌、队列上移/移除和清空确认", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  // Tab 遍历可能超过短素材的 3 秒；专用的 60 秒音频长于用例默认超时，避免自然结束抢先推进队列。
  // 仍由真实 Audio 解码播放，不替换媒体事件；其他用例保留短素材以验证自然结束与歌词。
  // 素材由 scripts/ffmpeg-pinned.sh 指定的编码器生成：
  // -f lavfi -i anullsrc=r=44100:cl=stereo -t 60 -ac 2 -fflags +bitexact -flags:a +bitexact -c:a flac -sample_fmt s16 -map_metadata -1 keyboard-60s.flac
  const { release } = await installPlayback(page, mock, false, {
    body: readFileSync(new URL("./support/audio/keyboard-60s.flac", import.meta.url)),
    durationSeconds: 60,
  });
  const third = release.media[0]?.tracks[2];
  if (!third) throw new Error("专项素材缺少第三首");
  await keyboardActivate(page, page.getByRole("button", { name: "播放整张专辑", exact: true }));
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await expect.poll(async () => (await media(page))?.duration).toBe(60);
  await keyboardActivate(page, bar(page).getByRole("button", { name: "暂停", exact: true }));
  await expect.poll(async () => (await media(page))?.paused).toBe(true);
  const firstSrc = (await media(page))?.src;
  await keyboardActivate(page, page.locator("#main ol > li").nth(2).getByRole("button", { name: "更多操作", exact: true }));
  await page.getByRole("menuitem", { name: "下一首播放", exact: true }).press("Enter");
  await expect(bar(page).getByRole("button", { name: "队列（19）", exact: true })).toBeVisible();
  expect(await media(page)).toMatchObject({ src: firstSrc, paused: true });
  await keyboardActivate(page, bar(page).getByRole("button", { name: "下一首", exact: true }));
  await expect.poll(() => mock.zones.currentTrack()).toBe(third.id);
  await expect.poll(async () => (await media(page))?.src).not.toBe(firstSrc);
  await expect.poll(async () => (await media(page))?.paused).toBe(false);
  await keyboardActivate(page, bar(page).getByRole("button", { name: "暂停", exact: true }));
  await expect.poll(async () => (await media(page))?.paused).toBe(true);
  await keyboardActivate(page, bar(page).getByRole("button", { name: "队列（19）", exact: true }));
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

/** 专辑页开始播放后打开全屏；歌词由全屏播放器展示。 */
async function openFullscreen(page: Page) {
  await startAndPause(page);
  await bar(page).getByRole("button", { name: "打开全屏播放器", exact: true }).click();
  await expect(fullscreenDialog(page)).toBeVisible();
  // 可见不代表 220ms 的 grow 已结束；缩放期间分次测量会混用不同帧的坐标。
  await expect(fullscreenDialog(page).locator(":scope > div").first()).toHaveCSS("transform", "none");
}

const lyricsPanel = (page: Page) => fullscreenDialog(page).getByRole("region", { name: "歌词", exact: true });

async function setMediaTime(page: Page, seconds: number) {
  await page.evaluate((time) => {
    const observed = Reflect.get(window, "playerMockObservedAudio") as HTMLAudioElement[];
    const audio = observed.find((item) => item.getAttribute("src")?.includes("/streams/")) ?? observed.at(-1);
    if (audio) audio.currentTime = time;
  }, seconds);
}

/** 768px 以上是“唱片 | 歌词”两栏；窄屏切换到歌词时，标题位于歌词下方。 */
async function expectLyricsLayout(page: Page, label: string) {
  const full = fullscreenDialog(page);
  const lyrics = lyricsPanel(page);
  const title = full.getByRole("heading", { level: 2, name: "合成曲目 1", exact: true });
  const [lyricsBox, titleBox] = await Promise.all([lyrics.boundingBox(), title.boundingBox()]);
  if (lyricsBox === null || titleBox === null) throw new Error(`${label}：缺少布局元素`);
  const viewport = page.viewportSize();
  if ((viewport?.width ?? 0) >= 768) {
    const coverBox = await full.getByLabel("没有可用封面").locator("xpath=../../..").boundingBox();
    if (coverBox === null) throw new Error(`${label}：缺少唱片`);
    expect(lyricsBox.x, `${label}：歌词在唱片右侧`).toBeGreaterThanOrEqual(coverBox.x + coverBox.width - 1);
    expect(titleBox.x, `${label}：标题在右侧信息栏`).toBeGreaterThanOrEqual(coverBox.x + coverBox.width - 1);
    expect(titleBox.y, `${label}：标题在歌词上方`).toBeLessThanOrEqual(lyricsBox.y + 1);
    expect(lyricsBox.height, `${label}：歌词与唱片共用上方两栏`).toBeGreaterThanOrEqual(280);
  } else {
    expect(lyricsBox.y, `${label}：歌词位于标题上方`).toBeLessThanOrEqual(titleBox.y + 1);
  }
}

for (const width of widths) {
  for (const kind of ["synced", "plain", "none"] as const) {
    test(`${width}px：${{ synced: "同步", plain: "纯文本", none: "没有" }[kind]}歌词的全屏布局不溢出，axe 无严重问题`, async ({ page, backend }) => {
      await page.setViewportSize({ width, height: 900 });
      const mock = await backend.start({ signedInAs: "listener" });
      await installPlayback(page, mock);
      await mockLyrics(page, lyricsReplies[kind]);
      await openFullscreen(page);
      const lyrics = lyricsPanel(page);
      if (width < 768) {
        await fullscreenDialog(page).getByRole("button", { name: "显示歌词", exact: true }).click();
      }
      await expect(lyrics).toBeVisible();
      if (kind === "synced") await expect(lyrics.getByRole("listitem")).toHaveCount(LYRIC_LINES);
      if (kind === "plain") await expect(lyrics.getByRole("group", { name: "歌词文本", exact: true })).toBeVisible();
      for (const removedText of ["内嵌歌词 · 逐行同步 · 点任一行跳到该处", "内嵌歌词 · 逐行同步", "内嵌歌词 · 纯文本"]) await expect(lyrics).not.toContainText(removedText);
      if (kind === "none") await expect(lyrics.getByText("这首曲目没有内嵌歌词", { exact: true })).toBeVisible();
      await expectLyricsLayout(page, `${width}px ${kind}`);
      await expectNoHorizontalOverflow(page, `${kind}歌词`);
      await expectNoDocumentScroll(page, `${kind}歌词`);
      // 明确检查悬停提示的最终状态，避免指针偶然停在控件上时 axe 采到 100ms 淡入的中间颜色。
      await fullscreenDialog(page).getByRole("button", { name: "收起全屏播放器", exact: true }).hover();
      const tooltip = fullscreenDialog(page).getByText("收起全屏播放器", { exact: true });
      await expect(tooltip).toBeVisible();
      await expect(tooltip).toHaveCSS("opacity", "1");
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
  await openFullscreen(page);
  const lyrics = lyricsPanel(page);
  const scroller = lyrics.getByRole("group", { name: "逐行歌词", exact: true });
  const items = lyrics.getByRole("listitem");
  const current = lyrics.locator("[aria-current='true']");
  await expect(items).toHaveCount(LYRIC_LINES);
  // 全屏打开前真实播放后暂停，位置可能越过第 1 行窗口，先归零再断言。
  await setMediaTime(page, 0);
  for (const removedText of ["内嵌歌词 · 逐行同步 · 点任一行跳到该处", "内嵌歌词 · 逐行同步", "内嵌歌词 · 纯文本"]) await expect(lyrics).not.toContainText(removedText);
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
  if (heightBefore === undefined) throw new Error("歌词列表没有可测量的高度");
  const plainMetrics = await metrics(items.nth(3));
  await setMediaTime(page, 1.55);
  await expect(current).toContainText("第 16 行歌词");
  await expect(current).toHaveCount(1);
  await expect.poll(() => inside(current), { message: "当前行被滚进歌词区域" }).toBe(true);
  expect(await metrics(current)).toEqual(plainMetrics);
  expect(await listHeight()).toBeCloseTo(heightBefore, 0);
  expect(await scroller.evaluate((element) => element.scrollTop), "已滚动到当前行附近").toBeGreaterThan(0);

  // 点击第 25 行（2.4 秒）：命令定位到曲目内位置；新流从 0 起，歌词按分段偏移显示该行。
  await lyrics.getByRole("button", { name: /^第 25 行歌词/ }).click();
  await expect(current).toContainText("第 25 行歌词");
  expect(mock.callsTo("POST", /\/commands$/).at(-1)?.body).toEqual({ command: "seek", position_seconds: 2.4 });

  // 键盘：焦点在当前行按钮上，方向键上移两行，Enter 跳转到第 23 行（2.2 秒）；焦点环可见。
  await lyrics.getByRole("button", { name: /^第 25 行歌词/ }).focus();
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");
  await expect(lyrics.getByRole("button", { name: /^第 23 行歌词/ })).toBeFocused();
  await expectVisibleFocusRing(page, "歌词行键盘操作");
  await page.keyboard.press("Enter");
  await expect(current).toContainText("第 23 行歌词");
  expect(mock.callsTo("POST", /\/commands$/).at(-1)?.body).toEqual({ command: "seek", position_seconds: 2.2 });
  await expectNoSeriousViolations(page, "同步歌词的当前行与焦点");
});

test("同步歌词：手动滚动后暂停跟随并出现“回到当前行”，点击后恢复并定位", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  await installPlayback(page, mock);
  await mockLyrics(page, lyricsReplies.synced);
  await openFullscreen(page);
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
  await openFullscreen(page);
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

test("已分析曲目显示波形，进度条仍可键盘定位", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const { group } = await installPlayback(page, mock);
  await page.route("**/api/v1/tracks/*/analysis", async (route) => {
    const trackId = new URL(route.request().url()).pathname.split("/").at(-2) ?? "";
    const body = sampleBody("tracks/analysis-done") as Record<string, unknown>;
    await route.fulfill({ json: { ...body, track_id: trackId } });
  });
  await startAndPause(page);
  await expect(bar(page).locator("canvas")).toBeVisible();
  const seek = bar(page).getByRole("slider", { name: "播放进度", exact: true });
  await expect(seek).toBeEnabled();
  await seek.focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => mock.callsTo("POST", /\/commands$/).at(-1)?.body).toMatchObject({ command: "seek", position_seconds: expect.any(Number) });
  await expect(seek).not.toHaveValue("0");
  await expect(page).toHaveURL(new RegExp(`/albums/${group.id}`));
});

for (const width of [375, 1440]) {
  test(`${width}px：播放设置页无横向溢出且无严重 axe 问题`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    await backend.start({ signedInAs: "listener" });
    await page.goto("/settings/playback");
    await expect(page.getByRole("heading", { level: 1, name: "播放", exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page, "播放设置");
    await expectNoSeriousViolations(page, "播放设置");
  });
}
