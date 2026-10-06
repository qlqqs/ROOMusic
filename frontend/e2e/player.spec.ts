import { readFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
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
  await page.route("**/api/v1/playback/formats", (route) => route.fulfill({ json: { items: [{ source_kind: "flac_vorbis_comment", codec: "flac", mime_type: "audio/flac", codecs: null }] } }));
  await page.route("**/api/v1/tracks/*/playback", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2);
    return route.fulfill({ json: { track_id: id, representation: { kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: flac.length } } });
  });
  await page.route("**/api/v1/tracks/*/audio", async (route) => {
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
  });
  // 只观察原生 Audio 引用；代理构造器仍返回真实 HTMLAudioElement，不改变任何行为。
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
