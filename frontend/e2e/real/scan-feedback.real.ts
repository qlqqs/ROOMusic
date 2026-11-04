import { mkdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";
import { decodeRecentlyAdded } from "../../src/features/catalog/api/decoders";
import { decodeScan, decodeScanReport, type ScanDTO } from "../../src/features/library/api/decoders";
import { body, catalog, decodeError, fixtureRoot, login, post, prepareLibrary, syntheticWav } from "./support";

/** 只观察真实后端快照；短扫描允许第一次 GET 已结束，不伪造中间阶段或延迟生产扫描。 */
async function observeScan(api: APIRequestContext) {
  const snapshots: ScanDTO[] = [decodeScan(await body(await post(api, "/api/v1/scans", {})))];
  const started = snapshots[0];
  if (started === undefined) throw new Error("启动扫描没有返回快照");
  await expect.poll(async () => {
    const snapshot = decodeScan(await body(await api.get(`/api/v1/scans/${started.id}`)));
    snapshots.push(snapshot);
    return snapshot.status;
  }, { intervals: [100], timeout: 30_000 }).not.toBe("running");
  let previous = 0;
  for (const snapshot of snapshots) {
    expect(snapshot.progress, "新扫描必须提供真实进度").not.toBeNull();
    expect(snapshot.progress).toBeDefined();
    const progress = snapshot.progress;
    if (progress == null) throw new Error("缺少扫描进度");
    expect(progress.percent_bp, "相邻快照百分比不能倒退").toBeGreaterThanOrEqual(previous);
    if (snapshot.status === "running") expect(progress.percent_bp).toBeLessThan(10_000);
    previous = progress.percent_bp;
  }
  const finished = snapshots.at(-1);
  if (finished === undefined) throw new Error("没有终态快照");
  expect(finished.status).toBe("succeeded");
  expect(finished.progress?.percent_bp).toBe(10_000);
  expect(finished.report_status).toBe("ready");
  return { finished, snapshots };
}

test("两张专辑三首合成 WAV：真实扫描 100%、报告计数、最近新增与成员权限", async ({ page, request, browser }, info) => {
  // 与现有真实入口同一安全边界：只在本轮临时目录写文件。
  expect(basename(fixtureRoot)).toBe("library");
  expect(basename(dirname(fixtureRoot))).toMatch(/^roomusic-playback-e2e\.[A-Za-z0-9]+$/);
  const directory = join(fixtureRoot, "scan-feedback");
  await prepareLibrary(request);
  const baseline = await observeScan(request);
  const initialFiles = baseline.finished.progress?.files_total;
  expect(initialFiles).not.toBeNull();
  if (initialFiles == null) throw new Error("基线扫描缺少文件总数");
  const titles = ["Scan Feedback Alpha", "Scan Feedback Beta"];
  try {
    for (const [album, tracks] of [[titles[0], [0x31, 0x32]], [titles[1], [0x43]]] as const) {
      if (album === undefined) throw new Error("缺少合成专辑名称");
      const albumDir = join(directory, album);
      await mkdir(albumDir, { recursive: true });
      for (const [index, fill] of tracks.entries()) {
        await writeFile(join(albumDir, `${index + 1}.wav`), syntheticWav({ title: `Track ${index + 1}`, album, artist: "Scan Feedback Artist" }, fill));
      }
    }
    const result = await observeScan(request);
    await info.attach("真实扫描快照", { body: JSON.stringify(result.snapshots, null, 2), contentType: "application/json" });
    expect(result.finished.progress).toMatchObject({ files_total: initialFiles + 3, files_processed: initialFiles + 3, count_complete: true, files_changed: false });
    const report = decodeScanReport(await body(await request.get(`/api/v1/scans/${result.finished.id}/report`)));
    expect(report).toMatchObject({
      scan_run_id: result.finished.id, status: "ready", scan_status: "succeeded", missing_reconciled: true,
      files: { counted: initialFiles + 3, total: initialFiles + 3, processed: initialFiles + 3, count_complete: true, changed: false },
      albums: { added: 2, updated: 0, missing: 0 },
      tracks: { added: 3, updated: 0, missing: 0 },
      issues: { recorded: 0, may_be_truncated: false },
      added_albums: { pagination: { total: 2 } },
    });
    expect(report.added_albums.items.map((item) => item.title_at_scan).sort()).toEqual(titles);
    const albums = (await catalog(request)).filter((item) => titles.includes(item.release.title));
    expect(albums).toHaveLength(2);
    expect(albums.reduce((total, item) => total + item.release.media.flatMap((medium) => medium.tracks).length, 0)).toBe(3);
    expect(report.added_albums.items.map((item) => item.current_group_id).sort()).toEqual(albums.map((item) => item.groupId).sort());

    await login(page.request);
    await page.goto(`/library/scans/${result.finished.id}`);
    await expect(page.getByRole("progressbar", { name: "扫描进度" })).toHaveAttribute("aria-valuenow", "100");
    const panel = page.getByRole("region", { name: "扫描报告" });
    const table = panel.getByRole("table", { name: "专辑与曲目的变化" });
    await expect(table.getByRole("row", { name: /^专辑/ }).getByRole("cell")).toHaveText(["2", "0", "0"]);
    await expect(table.getByRole("row", { name: /^曲目/ }).getByRole("cell")).toHaveText(["3", "0", "0"]);
    for (const title of titles) await expect(panel.getByRole("link", { name: title, exact: true })).toBeVisible();
    const screenshot = info.outputPath("scan-feedback-real-report.png");
    await page.screenshot({ path: screenshot, fullPage: true });
    await info.attach("真实报告", { path: screenshot, contentType: "image/png" });

    // 原样重扫不把 SQL 写入次数误报成变化，历史新增报告仍保留原计数。
    const rescanned = await observeScan(request);
    const unchanged = decodeScanReport(await body(await request.get(`/api/v1/scans/${rescanned.finished.id}/report`)));
    expect(unchanged).toMatchObject({
      status: "ready", missing_reconciled: true,
      albums: { added: 0, updated: 0, missing: 0 },
      tracks: { added: 0, updated: 0, missing: 0 },
      added_albums: { items: [], pagination: { total: 0 } },
    });
    await info.attach("原样重扫报告", { body: JSON.stringify(unchanged, null, 2), contentType: "application/json" });
    const historical = decodeScanReport(await body(await request.get(`/api/v1/scans/${result.finished.id}/report`)));
    expect(historical.albums).toEqual(report.albums);
    expect(historical.tracks).toEqual(report.tracks);

    const member = { username: `scan-member-${crypto.randomUUID().slice(0, 8)}`, password: "ScanFeedbackPassword42" };
    await body(await post(request, "/api/v1/users", member));
    const context = await browser.newContext({ baseURL: new URL(page.url()).origin });
    try {
      await login(context.request, member);
      const detail = decodeScan(await body(await context.request.get(`/api/v1/scans/${result.finished.id}`)));
      expect(detail.progress?.percent_bp).toBe(10_000);
      expect(detail.root_progress).not.toHaveProperty("id");
      expect(detail.root_progress).not.toHaveProperty("name");
      const forbidden = await context.request.get(`/api/v1/scans/${result.finished.id}/report`);
      expect(forbidden.status()).toBe(403);
      expect(decodeError(await forbidden.json()).error.code).toBe("forbidden");
      const recent = decodeRecentlyAdded(await body(await context.request.get("/api/v1/release-groups/recently-added")));
      for (const album of albums) expect(recent.items.map((item) => item.id)).toContain(album.groupId);
      const memberPage = await context.newPage();
      await memberPage.goto("/");
      const recentPanel = memberPage.getByRole("region", { name: "最近新增" });
      for (const title of titles) await expect(recentPanel.getByRole("link", { name: new RegExp(title) })).toBeVisible();
      await expect(memberPage.getByRole("link", { name: "扫描目录", exact: true })).toHaveCount(0);
    } finally {
      await context.close();
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
    await observeScan(request);
  }
});
