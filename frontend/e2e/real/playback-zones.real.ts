import { expect, test, type Page } from "@playwright/test";
import { decode as d } from "../../src/shared/api";
import { body, catalog, observeMedia, prepareLibrary, scan } from "./support";

// Node 中只读验收需要的字段，避免业务 decoder 经组件入口加载 CSS；完整 DTO 映射由 Vitest 覆盖。
const decodeSnapshot = d.defineDecoder(d.object({
  devices: d.array(d.object({ device_id: d.uuid(), zone_id: d.uuid(), online: d.boolean(), output_session_id: d.nullable(d.uuid()) }), { max: 10_000 }),
  zones: d.array(d.object({
    zone_id: d.uuid(), intent: d.oneOf(["play", "pause"] as const),
    current: d.nullable(d.object({ track: d.object({ track_id: d.uuid() }) })),
    stream: d.nullable(d.object({ url: d.string(), segments: d.array(d.object({ track_start_seconds: d.number() }), { max: 10_000 }) })),
  }), { max: 10_000 }),
}));

/** 只观察原生媒体：排除手势解锁用的静音片段，不替换播放、解码或时间推进。 */
async function sounding(page: Page) {
  return page.evaluate(() => window.playbackTestMedia
    .filter((audio) => /\/api\/v1\/me\/playback\/zones\/[^/]+\/streams\//.test(audio.currentSrc)
      && !audio.paused && !audio.ended && audio.readyState >= 2 && !audio.muted && audio.volume > 0)
    .map((audio) => ({ src: audio.currentSrc, seconds: audio.currentTime })));
}

test("同一浏览器两个标签页只有一个出声，关闭出声页后另一页从原位置接着放", async ({ page, context }) => {
  await prepareLibrary(context.request);
  await scan(context.request);
  const albums = await catalog(context.request);
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
