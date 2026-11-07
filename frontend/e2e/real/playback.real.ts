import { appendFile, copyFile, mkdir, rm, utimes } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { decode as d } from "../../src/shared/api";
import { decodeTrackLyrics, decodeTrackPlayback, decodeTrackPreparing } from "../../src/features/player/api/decoders";
import { admin, body, catalog, clearPlayHistory, decodeError, decodeID, decodeRoot, decodeSetup, findTrack, fixtureRoot, login, mediaState, observeMedia, origin, playsPath, playTrack, post, recentReleases, scan, type Albums } from "./support";

let albums: Albums;
let root: ReturnType<typeof decodeRoot>;

test.beforeAll(async ({ request }) => {
  // 文件写操作只允许脚本本轮生成的临时根，绝不接受真实音乐目录。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  const setup = decodeSetup(await body(await request.get("/api/v1/setup/status")));
  if (setup.setup_required) await body(await post(request, "/api/v1/setup/admin", admin));
  await login(request);
  root = decodeRoot(await body(await post(request, "/api/v1/library-roots", { path: fixtureRoot })));
  await scan(request);
  albums = await catalog(request);
  expect(albums.flatMap((album) => album.release.media.flatMap((medium) => medium.tracks))).toHaveLength(13);
});

test.beforeEach(async ({ page }) => {
  await login(page.request);
  await observeMedia(page);
});

for (const source of ["flac-16-44100.flac", "flac-24-48000.flac", "mp3-44100.mp3", "wav-16-44100.wav", "aac-44100.m4a", "vorbis-44100.ogg", "opus-48000.opus"]) {
  test(`${source} 由 Chromium 真实解码，媒体时间随真实时间推进并自然结束`, async ({ page }) => {
    const item = findTrack(albums, source);
    const audioResponses: number[] = [];
    page.on("response", (response) => {
      if (response.url().endsWith(`/tracks/${item.track.id}/audio`)) audioResponses.push(response.status());
    });
    await playTrack(page, item);
    const start = await mediaState(page);
    expect(start).not.toBeNull();
    const wallStart = Date.now();
    await expect.poll(async () => (await mediaState(page))?.time ?? 0, { intervals: [50] }).toBeGreaterThan((start?.time ?? 0) + 0.2);
    const elapsed = (Date.now() - wallStart) / 1000;
    const advance = (await mediaState(page))?.time ?? 0;
    expect(advance - (start?.time ?? 0)).toBeLessThan(elapsed + 0.5);
    await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "ended" && event.src.includes(id)), item.track.id)).toBe(true);
    expect(audioResponses.some((status) => status === 200 || status === 206)).toBe(true);
    expect(await page.evaluate((id) => window.playbackTestEvents.filter((event) => event.src.includes(id) && event.type === "error"), item.track.id)).toEqual([]);
  });
}

test("FLAC 真实 seek，自然切到下一首；切页不断播，刷新暂停恢复位置", async ({ page }) => {
  const first = findTrack(albums, "flac-16-44100.flac");
  const second = findTrack(albums, "flac-24-48000.flac");
  await page.goto(`/albums/${first.groupId}`);
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(false);
  await page.getByRole("button", { name: "暂停", exact: true }).click();
  const seek = page.getByRole("slider", { name: "播放进度", exact: true });
  await expect(seek).toBeEnabled();
  const bounds = await seek.boundingBox();
  if (!bounds) throw new Error("播放进度没有可操作区域");
  // 真实鼠标拖动原生 slider，不直接修改媒体时间或调用引擎。
  await page.mouse.move(bounds.x + 8, bounds.y + bounds.height / 2);
  await page.mouse.down();
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2, { steps: 6 });
  await page.mouse.up();
  await expect.poll(() => page.evaluate(() => window.playbackTestEvents.some((event) => event.type === "seeked" && event.time > 1))).toBe(true);
  // 原生媒体 seekable 区间与实际位置，不能只检查 slider 的视觉数值。
  await expect.poll(async () => (await mediaState(page))?.time ?? 0).toBeGreaterThan(1);
  const position = (await mediaState(page))?.time ?? 0;
  await page.reload();
  await expect(page.getByRole("button", { name: "播放", exact: true }).last()).toBeVisible();
  // 刷新恢复不得自动触发原生 play()。
  expect(await page.evaluate(() => window.playbackTestMedia.length)).toBe(0);
  await page.getByRole("button", { name: "播放", exact: true }).last().click();
  await expect.poll(async () => (await mediaState(page))?.time ?? 0).toBeGreaterThanOrEqual(position - 0.2);
  await page.getByRole("link", { name: "正在播放", exact: true }).click();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(false);
  await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "ended" && event.src.includes(id)), first.track.id)).toBe(true);
  await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "playing" && event.src.includes(id)), second.track.id)).toBe(true);
});

// ALAC 由服务端用固定版本 ffmpeg 无损转换成 FLAC 后交付（playback-e2e.sh 提供 ROOMUSIC_FFMPEG_PATH）。
// 音频请求从不启动转换，转换之前是 409；之后必须先预检（预检是唯一启动或加入转换的入口）。
// 播放 formats 专辑里的曲目会预取下一首派生曲目（别的用例可能已经转换过 ALAC），所以这里用一个专用的探针文件：
// 放在独立目录（独立专辑，不进入任何其他用例的队列），修改时间也与原素材不同，没有任何预取或缓存命中会先于本用例转换它。
test("ALAC 音频请求不会启动转换：转换前 409，预检完成后才能读取 audio/flac，并支持 Range", async ({ page }) => {
  const probeDirectory = join(fixtureRoot, "probe");
  const probeFile = join(probeDirectory, "alac-probe.m4a");
  try {
    await mkdir(probeDirectory, { recursive: true });
    await copyFile(join(fixtureRoot, "formats", "alac-16-44100.m4a"), probeFile);
    await utimes(probeFile, new Date(), new Date(Date.now() - 86_400_000));
    await scan(page.request);
    const item = findTrack(await catalog(page.request), "alac-probe.m4a");
    const audio = `/api/v1/tracks/${item.track.id}/audio`;
    const before = await page.request.get(audio);
    expect(before.status()).toBe(409);
    expect(decodeError(await before.json()).error.code).toBe("derivation_not_ready");
    const head = await page.request.head(audio);
    expect(head.status()).toBe(409);
    // 预检会等待转换完成（合成素材约 1 秒）：返回 200 与派生表示。
    const preflight = await page.request.get(`/api/v1/tracks/${item.track.id}/playback`);
    expect(preflight.status()).toBe(200);
    const ready = decodeTrackPlayback(await preflight.json());
    expect(ready.representation).toMatchObject({ kind: "derived", mime_type: "audio/flac", codecs: null, url: audio });
    const full = await page.request.get(audio);
    expect(full.status()).toBe(200);
    expect(full.headers()["content-type"]).toBe("audio/flac");
    expect(full.headers()["cache-control"]).toBe("private, no-store");
    expect(full.headers()["accept-ranges"]).toBe("bytes");
    expect(full.headers()["etag"]).toMatch(/^"[0-9a-f]{32}"$/);
    const content = await full.body();
    expect(content.length).toBe(ready.representation.size_bytes);
    expect(content.subarray(0, 4).toString("latin1")).toBe("fLaC");
    const partial = await page.request.get(audio, { headers: { Range: "bytes=0-3" } });
    expect(partial.status()).toBe(206);
    expect((await partial.body()).toString("latin1")).toBe("fLaC");
    expect(partial.headers()["etag"]).toBe(full.headers()["etag"]);
    const outOfRange = await page.request.get(audio, { headers: { Range: `bytes=${content.length}-` } });
    expect(outOfRange.status()).toBe(416);
  } finally {
    // 探针由本用例拥有；删除后重扫，避免后续 worker 初始化时多出一首。
    await rm(probeDirectory, { recursive: true, force: true });
    await scan(page.request);
  }
});

for (const source of ["alac-16-44100.m4a", "alac-24-48000.m4a"]) {
  test(`${source} 经服务端无损转换：Chromium 真实解码 FLAC，真实拖动，自然结束，信号路径写明无损转换`, async ({ page }) => {
    const item = findTrack(albums, source);
    const preflights: { status: number; kind: string }[] = [];
    const audioResponses: { status: number; type: string; etag: string }[] = [];
    page.on("response", (response) => {
      const url = response.url();
      if (url.endsWith(`/tracks/${item.track.id}/playback`)) {
        void response.json().then((input: unknown) => {
          preflights.push({ status: response.status(), kind: response.status() === 202 ? decodeTrackPreparing(input).status : decodeTrackPlayback(input).representation.kind });
        }, () => undefined);
      }
      if (url.endsWith(`/tracks/${item.track.id}/audio`)) audioResponses.push({ status: response.status(), type: response.headers()["content-type"] ?? "", etag: response.headers()["etag"] ?? "" });
    });
    await playTrack(page, item);
    expect(await mediaState(page)).not.toBeNull();
    await expect.poll(() => preflights.some((preflight) => preflight.status === 200 && preflight.kind === "derived")).toBe(true);
    expect(audioResponses.length).toBeGreaterThan(0);
    for (const response of audioResponses) {
      expect([200, 206]).toContain(response.status);
      expect(response.type).toBe("audio/flac");
      expect(response.etag).toMatch(/^"[0-9a-f]{32}"$/);
    }
    // 信号路径的服务端一栏：无损转换，不再是原文件直出（趁这首还是当前曲目：自然结束后队列会前进到别的格式）。
    await page.getByRole("link", { name: "正在播放", exact: true }).click();
    const server = page.getByRole("heading", { name: "服务端", exact: true }).locator("..");
    await expect(server).toContainText("无损转换");
    await expect(server.getByText("ALAC 转成 FLAC，位深、采样率与声道不变。", { exact: true })).toBeVisible();
    await expect(server).not.toContainText("PCM 一致");
    await expect(server).not.toContainText("原文件直出");
    // 真实鼠标拖动原生 slider：暂停后拖到中间，媒体在派生文件上真实 seek。播放栏在每个页面都有，限定在它里面以免与正在播放页的控件重名。
    const bar = page.getByRole("region", { name: "播放器", exact: true });
    await bar.getByRole("button", { name: "暂停", exact: true }).click();
    const seek = bar.getByRole("slider", { name: "播放进度", exact: true });
    await expect(seek).toBeEnabled();
    const bounds = await seek.boundingBox();
    if (!bounds) throw new Error("播放进度没有可操作区域");
    await page.mouse.move(bounds.x + 8, bounds.y + bounds.height / 2);
    await page.mouse.down();
    await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2, { steps: 6 });
    await page.mouse.up();
    await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "seeked" && event.src.includes(id) && event.time > 0.2), item.track.id)).toBe(true);
    await bar.getByRole("button", { name: "播放", exact: true }).click();
    await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "ended" && event.src.includes(id)), item.track.id)).toBe(true);
    expect(await page.evaluate((id) => window.playbackTestEvents.filter((event) => event.src.includes(id) && event.type === "error"), item.track.id)).toEqual([]);
  });
}

// 内嵌歌词：scripts/gen-playback-fixtures.sh 把同一份同步歌词写进 flac-16-44100.flac（Vorbis 注释 LYRICS）与 alac-16-44100.m4a（©lyr）；
// 这里经真实扫描、接口与页面核对。歌词内容与 playback_fixture_test.go 的 fixtureLyrics 一致：两行元标签不显示，第二个时间点有两行（一组）。
const fixtureLyricsLines = [
  { start_ms: 0, text: "Fixture line one" },
  { start_ms: 800, text: "Fixture line two" },
  { start_ms: 800, text: "夹具第二行" },
  { start_ms: 1600, text: "Fixture line three" },
];

for (const source of ["flac-16-44100.flac", "alac-16-44100.m4a"]) {
  test(`${source} 的内嵌歌词经真实扫描提取，接口返回解析后的同步歌词`, async ({ page }) => {
    const item = findTrack(albums, source);
    const response = await page.request.get(`/api/v1/tracks/${item.track.id}/lyrics`);
    expect(response.status()).toBe(200);
    expect(response.headers()["cache-control"]).toBe("no-store");
    const lyrics = decodeTrackLyrics(await response.json());
    expect(lyrics.track_id).toBe(item.track.id);
    expect(lyrics.lyrics).toEqual({ kind: "synced", source: "embedded", lines: fixtureLyricsLines });
  });
}

test("没有内嵌歌词的素材返回 null；CUE 虚拟轨没有歌词，即使整轨镜像文件带着歌词", async ({ page }) => {
  for (const source of ["flac-24-48000.flac", "mp3-44100.mp3", "vorbis-44100.ogg"]) {
    const response = await page.request.get(`/api/v1/tracks/${findTrack(albums, source).track.id}/lyrics`);
    expect(response.status(), source).toBe(200);
    expect(decodeTrackLyrics(await response.json()).lyrics, source).toBeNull();
  }
  // cue/image.flac 是 flac-16-44100.flac 的副本（带歌词）：整张镜像的歌词不能分给它的虚拟轨。
  const virtualTracks = albums.flatMap((album) => album.release.media.flatMap((medium) => medium.tracks)).filter((track) => track.source_kind === "cue_virtual");
  expect(virtualTracks).toHaveLength(2);
  for (const track of virtualTracks) {
    const response = await page.request.get(`/api/v1/tracks/${track.id}/lyrics`);
    expect(response.status()).toBe(200);
    expect(decodeTrackLyrics(await response.json()).lyrics).toBeNull();
  }
});

for (const source of ["flac-16-44100.flac", "alac-16-44100.m4a"]) {
  test(`${source}：正在播放页显示歌词，点击一行后真实播放位置跳到该行，继续播放时当前行前进`, async ({ page }) => {
    const item = findTrack(albums, source);
    await playTrack(page, item);
    const bar = page.getByRole("region", { name: "播放器", exact: true });
    await bar.getByRole("button", { name: "暂停", exact: true }).click();
    await expect.poll(async () => (await mediaState(page))?.paused).toBe(true);
    await page.getByRole("link", { name: "正在播放", exact: true }).click();
    const lyrics = page.locator("#main").getByRole("region", { name: "歌词", exact: true });
    const current = lyrics.locator("[aria-current='true']");
    // 元标签不显示；同一时间点的两行是一组。
    await expect(lyrics.getByRole("listitem")).toHaveText(["Fixture line one", "Fixture line two夹具第二行", "Fixture line three"]);
    await expect(lyrics.getByText("内嵌歌词 · 逐行同步 · 点任一行跳到该处", { exact: true })).toBeVisible();

    // 真实点击：媒体元素真实 seek，播放位置不早于该行的时间。
    await lyrics.getByRole("button", { name: /^Fixture line three/ }).click();
    await expect.poll(async () => (await mediaState(page))?.time ?? 0).toBeGreaterThanOrEqual(1.55);
    await expect(current).toHaveText("Fixture line three");
    await lyrics.getByRole("button", { name: /^Fixture line two/ }).click();
    await expect.poll(async () => (await mediaState(page))?.time ?? 0).toBeGreaterThanOrEqual(0.75);
    expect((await mediaState(page))?.time ?? 0).toBeLessThan(1.5);
    await expect(current).toHaveText("Fixture line two夹具第二行");

    // 继续播放：位置随真实时间越过 1.6 秒，当前行前进到第三行。ALAC 素材只有 2 秒，第三行只当前不到半秒，轮询可能错过，
    // 所以在点“播放”之前开始记录当前行的变化序列，再核对序列。
    await lyrics.evaluate((region) => {
      const seen: string[] = [];
      Reflect.set(window, "lyricsCurrentSeen", seen);
      const record = () => {
        const text = region.querySelector("[aria-current='true']")?.textContent;
        if (text && seen.at(-1) !== text) seen.push(text);
      };
      record();
      new MutationObserver(record).observe(region, { subtree: true, childList: true, attributes: true, attributeFilter: ["aria-current"] });
    });
    await bar.getByRole("button", { name: "播放", exact: true }).click();
    await expect.poll(() => page.evaluate(() => Reflect.get(window, "lyricsCurrentSeen") as string[]), { timeout: 10_000 }).toEqual(["Fixture line two夹具第二行", "Fixture line three"]);
  });
}

// 播放历史（阶段 P）：真实服务、真实扫描、Chromium 真实播放与真实拖动。一次播放要累计出声达到 min(时长/2, 4 分钟) 才记录：
// 合成素材约 3 秒，门槛约 1.5 秒；slow.wav 长 180 秒，门槛 90 秒。别的用例播放过素材，所以每个用例先清空自己的历史。
/** 页面发出的“记录一次播放”请求（POST /api/v1/me/plays）的请求体。 */
function watchPlayReports(page: Page): { track_id: string; played_seconds: number }[] {
  const reports: { track_id: string; played_seconds: number }[] = [];
  page.on("request", (request) => {
    if (request.method() !== "POST" || new URL(request.url()).pathname !== playsPath) return;
    const input: unknown = request.postDataJSON();
    if (typeof input === "object" && input !== null && "track_id" in input && "played_seconds" in input && typeof input.track_id === "string" && typeof input.played_seconds === "number") {
      reports.push({ track_id: input.track_id, played_seconds: input.played_seconds });
    }
  });
  return reports;
}

test("播放过半后服务端记下这一次，概览的“最近播放”显示这张专辑，清除后为空", async ({ page }) => {
  await clearPlayHistory(page.request);
  expect(await recentReleases(page.request)).toEqual({ has_history: false, items: [] });
  const item = findTrack(albums, "flac-16-44100.flac");
  const duration = item.track.duration_seconds ?? 0;
  expect(duration, "合成素材应有扫描得到的时长").toBeGreaterThan(1);
  const threshold = Math.min(duration / 2, 240);
  const reports = watchPlayReports(page);

  await playTrack(page, item);
  // 出声累计达到门槛才上报：服务端出现这首曲目的记录（专辑里后面的曲目在它自然结束之后才开始，不会先于它）。
  await expect.poll(async () => (await recentReleases(page.request)).items.map((recent) => recent.last_track.id), { timeout: 15_000 }).toEqual([item.track.id]);
  await page.getByRole("region", { name: "播放器", exact: true }).getByRole("button", { name: "暂停", exact: true }).click();
  const own = reports.filter((report) => report.track_id === item.track.id);
  expect(own, "这一次访问只上报一次").toHaveLength(1);
  expect(own[0]?.played_seconds ?? 0).toBeGreaterThanOrEqual(threshold);
  expect(own[0]?.played_seconds ?? 0).toBeLessThanOrEqual(duration + 1);

  // 概览：最近播放里是这张专辑，整张卡片链接到播放过的版本。
  await page.goto("/");
  const panel = page.getByRole("region", { name: "最近播放", exact: true });
  const card = panel.getByRole("link", { name: item.release.title });
  await expect(card).toHaveAttribute("href", `/albums/${item.groupId}?release=${item.release.id}`);
  await expect(card).toContainText(/上次播放 \d{4}-\d{2}-\d{2} \d{2}:\d{2}/);
  const recent = await recentReleases(page.request);
  expect(recent.has_history).toBe(true);
  expect(recent.items).toHaveLength(1);
  expect(recent.items[0]).toMatchObject({ group_id: item.groupId, release: { id: item.release.id } });

  // 清除：确认后区块为空，服务端也没有了；刷新后仍然为空。
  await panel.getByRole("button", { name: "清除播放历史", exact: true }).click();
  await page.getByRole("dialog", { name: "清除播放历史？" }).getByRole("button", { name: "清除播放历史", exact: true }).click();
  await expect(panel.getByText("已清除你的播放历史。")).toBeVisible();
  expect(await recentReleases(page.request)).toEqual({ has_history: false, items: [] });
  await page.reload();
  await expect(panel.getByText(/^还没有播放记录。/)).toBeVisible();
  await expect(panel.getByRole("button", { name: "清除播放历史", exact: true })).toHaveCount(0);
});

// M5：放在末尾故意改坏源文件的用例之前，沿用本组每次先清空历史的隔离方式。
// 仍用真实服务与原生媒体事件；这里只解码断言所需的安全字段，不把应用样式带进 Node。
const decodeHistory = d.defineDecoder(d.object({
  items: d.array(d.object({ track_id: d.uuid(), title: d.string(), play_count: d.integer({ min: 1 }) }), { max: 100 }),
  has_history: d.boolean(),
  pagination: d.object({ total: d.integer({ min: 0 }) }),
}));

test("播放过半后历史页出现这首且次数为 1，从历史移除后页面与服务端都不再显示", async ({ page }) => {
  await clearPlayHistory(page.request);
  const item = findTrack(albums, "flac-16-44100.flac");
  const duration = item.track.duration_seconds ?? 0;
  expect(duration).toBeGreaterThan(1);
  const reports = watchPlayReports(page);
  await playTrack(page, item);
  const history = async () => decodeHistory(await body(await page.request.get(`${playsPath}/tracks`)));
  await expect.poll(async () => (await history()).items, { timeout: 15_000 }).toEqual([{ track_id: item.track.id, title: item.track.title, play_count: 1 }]);
  await page.getByRole("region", { name: "播放器", exact: true }).getByRole("button", { name: "暂停", exact: true }).click();
  expect(reports.filter((report) => report.track_id === item.track.id)).toHaveLength(1);
  expect(reports.find((report) => report.track_id === item.track.id)?.played_seconds ?? 0).toBeGreaterThanOrEqual(Math.min(duration / 2, 240));
  await page.goto("/me/history");
  const row = page.getByRole("table", { name: "播放历史" }).getByRole("row").filter({ has: page.getByText(item.track.title, { exact: true }) });
  await expect(row).toBeVisible();
  await expect(row.getByRole("cell", { name: "1", exact: true })).toBeVisible();
  await expect(row.getByRole("link", { name: item.release.title, exact: true })).toHaveAttribute("href", `/albums/${item.groupId}?release=${item.release.id}`);
  await row.getByRole("button", { name: "更多操作" }).click();
  await page.getByRole("menuitem", { name: "从历史中移除" }).click();
  const dialog = page.getByRole("dialog", { name: "从历史中移除这首曲目？" });
  await expect(dialog).toContainText("的播放记录会被移除，不能恢复。");
  await dialog.getByRole("button", { name: "从历史中移除", exact: true }).click();
  await expect(row).toHaveCount(0);
  await expect(page.getByText(/^还没有播放记录。/)).toBeVisible();
  expect(await history()).toEqual({ items: [], has_history: false, pagination: { total: 0 } });
  expect(await recentReleases(page.request)).toEqual({ items: [], has_history: false });
  await page.reload();
  await expect(page.getByText(/^还没有播放记录。/)).toBeVisible();
});

test("只拖动、几乎没有出声的播放不产生记录：拖到接近末尾再自然播完", async ({ page }) => {
  await clearPlayHistory(page.request);
  const item = findTrack(albums, "slow.wav"); // 180 秒，门槛 90 秒：拖到末尾后只剩几秒可播
  const reports = watchPlayReports(page);
  await playTrack(page, item);
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  const seek = bar.getByRole("slider", { name: "播放进度", exact: true });
  await expect(seek).toBeEnabled();
  const bounds = await seek.boundingBox();
  if (!bounds) throw new Error("播放进度没有可操作区域");
  // 真实鼠标把原生 slider 拖到最右端附近。
  await page.mouse.move(bounds.x + 8, bounds.y + bounds.height / 2);
  await page.mouse.down();
  await page.mouse.move(bounds.x + bounds.width - 3, bounds.y + bounds.height / 2, { steps: 6 });
  await page.mouse.up();
  await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "seeked" && event.src.includes(id) && event.time > 150), item.track.id)).toBe(true);
  await expect.poll(() => page.evaluate((id) => window.playbackTestEvents.some((event) => event.type === "ended" && event.src.includes(id)), item.track.id), { timeout: 30_000 }).toBe(true);
  // 位置走完了整首，但出声只有开头与结尾的几秒，远不到 90 秒：没有上报，服务端也没有记录。
  expect(reports).toEqual([]);
  expect((await recentReleases(page.request)).items.map((recent) => recent.last_track.id)).not.toContain(item.track.id);
});

// WAV extensible 与 CUE 虚拟轨仍是“不能直接播放”，不是“浏览器不能解码”。
test("CUE 虚拟轨和 WAV extensible 明确禁用且不会请求音频", async ({ page }) => {
  const reads: string[] = [];
  page.on("request", (request) => { if (/\/audio$/.test(request.url())) reads.push(request.url()); });
  for (const source of ["wav-24-48000.wav"]) {
    const item = findTrack(albums, source);
    await page.goto(`/albums/${item.groupId}`);
    const row = page.locator("ol > li").filter({ has: page.getByText(item.track.title, { exact: true }) });
    await expect(row.getByRole("button", { name: "从这首开始播放", exact: true })).toBeDisabled();
    await expect(row).toContainText(/不能直接播放|不支持.*直出|不支持.*直接/);
  }
  const cue = albums.find((album) => album.release.media.some((medium) => medium.tracks.some((track) => track.source_kind === "cue_virtual")));
  expect(cue).toBeDefined();
  if (!cue) throw new Error("扫描没有生成虚拟轨");
  await page.goto(`/albums/${cue.groupId}`);
  const virtualRows = page.locator("ol > li");
  await expect(virtualRows).toHaveCount(2);
  await expect(virtualRows.first().getByRole("button", { name: "从这首开始播放", exact: true })).toBeDisabled();
  await expect(virtualRows.first()).toContainText(/CUE/);
  expect(reads).toEqual([]);
});

test("另一标签页退出：本页停止并清除队列和刷新恢复数据", async ({ page, context }) => {
  const item = findTrack(albums, "slow.wav");
  await playTrack(page, item);
  const other = await context.newPage();
  await other.goto("/");
  await other.getByRole("region", { name: "当前用户", exact: true }).getByRole("button", { name: `${admin.username} 管理员`, exact: true }).click();
  await other.getByRole("menu", { name: "账号", exact: true }).getByRole("menuitem", { name: "退出登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(true);
  expect(await page.evaluate(() => window.playbackTestMedia.every((audio) => !audio.getAttribute("src")))).toBe(true);
  expect(await page.evaluate(() => Object.keys(sessionStorage).filter((key) => /player|playback/i.test(key)))).toEqual([]);
  await other.close();
});

test("管理员撤销会话后：新的预检与音频请求均返回 401 unauthorized", async ({ page, request }) => {
  await login(request);
  const created = await body(await post(request, "/api/v1/users", { username: "slow-listener", password: admin.password }));
  // 用户 ID 使用已存在的登录 decoder 验证，禁止未经检查的 JSON 强转。
  const user = decodeID(created);
  const listener = await login(page.request, { username: "slow-listener", password: admin.password });
  expect(listener.id).toBe(user.id);
  const item = findTrack(albums, "slow.wav");
  const playback = `/api/v1/tracks/${item.track.id}/playback`;
  const audio = `/api/v1/tracks/${item.track.id}/audio`;
  const before = await page.request.get(playback);
  expect(before.status()).toBe(200);
  expect(decodeTrackPlayback(await before.json())).toMatchObject({ track_id: item.track.id, representation: { kind: "original", url: audio } });
  const readable = await page.request.get(audio, { headers: { Range: "bytes=0-3" } });
  expect(readable.status()).toBe(206);
  expect((await readable.body()).toString("latin1")).toBe("RIFF");

  await body(await post(request, `/api/v1/users/${user.id}/sessions/revoke`, {}));
  // C1 只在新请求时核对会话；不再要求撤销切断已建立的传输。
  const deniedPreflight = await page.request.get(playback);
  expect(deniedPreflight.status()).toBe(401);
  expect(decodeError(await deniedPreflight.json()).error.code).toBe("unauthorized");
  const deniedAudio = await page.request.get(audio);
  expect(deniedAudio.status()).toBe(401);
  expect(decodeError(await deniedAudio.json()).error.code).toBe("unauthorized");
});

test("播放中停用目录：下一首显示所在目录已停用，专辑仍可浏览", async ({ page }) => {
  const first = findTrack(albums, "flac-16-44100.flac");
  await page.goto(`/albums/${first.groupId}`);
  await page.getByRole("button", { name: "播放整张专辑", exact: true }).click();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(false);
  const disabled = decodeRoot(await body(await page.request.patch(`/api/v1/library-roots/${root.id}`, {
    data: { status: "disabled", expected_revision: root.revision }, headers: { Origin: origin, "Idempotency-Key": crypto.randomUUID() },
  })));
  try {
    await page.getByRole("button", { name: "下一首", exact: true }).click();
    await expect(page.getByText(/目录已停用/).first()).toBeVisible();
    const denied = await page.request.get(`/api/v1/tracks/${first.track.id}/playback`);
    expect(denied.status()).toBe(409);
    expect(decodeError(await denied.json()).error.code).toBe("root_disabled");
    await expect(page.getByRole("heading", { name: first.release.title, exact: true })).toBeVisible();
  } finally {
    root = decodeRoot(await body(await post(page.request, `/api/v1/library-roots/${root.id}/restore`, { expected_revision: disabled.revision })));
  }
});

test("合成源文件扫描后改动：预检拒绝且界面提示需要重新扫描", async ({ page }) => {
  const item = findTrack(albums, "flac-16-44100.flac");
  await appendFile(join(fixtureRoot, "formats", "flac-16-44100.flac"), Buffer.from([0]));
  const changed = await page.request.get(`/api/v1/tracks/${item.track.id}/playback`);
  expect(changed.status()).toBe(409);
  expect(decodeError(await changed.json()).error.code).toBe("source_changed");
  await page.goto(`/albums/${item.groupId}`);
  const row = page.locator("ol > li").filter({ has: page.getByText(item.track.title, { exact: true }) });
  await row.getByRole("button", { name: "从这首开始播放", exact: true }).click();
  await page.getByRole("link", { name: "正在播放", exact: true }).click();
  await expect(page.getByText(/重新扫描/).first()).toBeVisible();
});
