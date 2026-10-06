import { appendFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import http from "node:http";
import { expect, test } from "@playwright/test";
import { admin, body, catalog, decodeError, decodeID, decodeRoot, decodeSetup, findTrack, fixtureRoot, login, mediaState, observeMedia, origin, playTrack, post, scan, type Albums } from "./support";

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

test("ALAC、CUE 虚拟轨和 WAV extensible 明确禁用且不会请求音频", async ({ page }) => {
  const reads: string[] = [];
  page.on("request", (request) => { if (/\/audio$/.test(request.url())) reads.push(request.url()); });
  for (const source of ["alac-16-44100.m4a", "alac-24-48000.m4a", "wav-24-48000.wav"]) {
    const item = findTrack(albums, source);
    await page.goto(`/albums/${item.groupId}`);
    const row = page.locator("ol > li").filter({ has: page.getByText(item.track.title, { exact: true }) });
    await expect(row.getByRole("button", { name: "从这首开始播放", exact: true })).toBeDisabled();
    await expect(row).toContainText(source.startsWith("alac") ? /浏览器.*不支持|浏览器.*无法|浏览器.*不能/ : /不能直接播放|不支持.*直出|不支持.*直接/);
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
  await other.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(true);
  expect(await page.evaluate(() => window.playbackTestMedia.every((audio) => !audio.getAttribute("src")))).toBe(true);
  expect(await page.evaluate(() => Object.keys(sessionStorage).filter((key) => /player|playback/i.test(key)))).toEqual([]);
  await other.close();
});

test("管理员撤销：真正受背压的长连接响应被截断，新请求返回401", async ({ page, request }) => {
  await login(request);
  const created = await body(await post(request, "/api/v1/users", { username: "slow-listener", password: admin.password }));
  // 用户 ID 使用已存在的登录 decoder 验证，禁止未经检查的 JSON 强转。
  await login(page.request, { username: "slow-listener", password: admin.password });
  const user = decodeID(created);
  const item = findTrack(albums, "slow.wav");
  const cookies = await page.context().cookies(origin);
  const cookie = cookies.map((entry) => `${entry.name}=${entry.value}`).join("; ");
  let clientTimedOut = false;
  const response = await new Promise<http.IncomingMessage>((resolve, reject) => {
    const req = http.get(`${origin}/api/v1/tracks/${item.track.id}/audio`, { headers: { Cookie: cookie } }, resolve);
    req.on("error", reject);
    req.setTimeout(30_000, () => { clientTimedOut = true; req.destroy(new Error("慢速传输超时")); });
  });
  let received = 0;
  response.pause();
  const completed = new Promise<boolean>((resolve) => {
    response.on("data", (chunk: Buffer) => { received += chunk.length; });
    response.on("end", () => resolve(true));
    response.on("error", () => resolve(false));
    response.on("aborted", () => resolve(false));
  });
  try {
    expect(response.statusCode).toBe(200);
    const length = Number(response.headers["content-length"]);
    expect(length).toBeGreaterThan(24 * 1024 * 1024);
    await body(await post(request, `/api/v1/users/${user.id}/sessions/revoke`, {}));
    const denied = await page.request.get(`/api/v1/tracks/${item.track.id}/audio`);
    expect(denied.status()).toBe(401);
    // 原生 TCP 客户端暂停接收超过生产复核间隔，然后释放背压。
    // 已交付缓冲不能收回；这里证明后续服务端读取被切断，不声称浏览器缓冲即时消失。
    await new Promise((resolve) => setTimeout(resolve, 11_000));
    const resumedAt = Date.now();
    response.resume();
    expect(await completed).toBe(false);
    expect(clientTimedOut, "客户端超时不能冒充服务端撤销").toBe(false);
    expect(Date.now() - resumedAt, "释放背压后应在复核间隔内切断").toBeLessThan(10_000);
    expect(received).toBeLessThan(length);
  } finally {
    response.destroy();
  }
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
