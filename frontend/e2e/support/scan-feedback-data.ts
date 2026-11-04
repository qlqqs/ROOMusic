import { decodeScan, decodeScanReport, type ScanDTO } from "../../src/features/library/api/decoders";
import { sampleBody } from "../../src/test/api-samples";
import { albumGroups } from "../../src/test/catalog-data";
import type { ApiMock } from "./api-mock";

export const feedbackWidths = [360, 390, 768, 1280, 1440] as const;
export const longRootName = `合成扫描目录${"UnbrokenDirectoryName".repeat(12)}`;
export const longAlbumTitle = `长专辑标题${"UnbrokenAlbumTitle".repeat(12)}`;
export const longArtist = `合成演奏者${"VeryLongArtistName".repeat(10)}`;

/** 复用真实接口样本，只放大安全显示字段与分页条目；不从诊断或时间推算进度。 */
export function seedScanFeedback(mock: ApiMock, count = 22) {
  mock.scanFinishesAfterReads = null;
  const groups = albumGroups(count, (index) => ({
    title: index === 1 ? longAlbumTitle : `新增专辑 ${String(index).padStart(2, "0")}`,
    artist: index === 1 ? longArtist : `合成演奏者 ${index}`,
  })).map((group, index) => ({ ...group, added_at: new Date(Date.UTC(2026, 0, 2, 0, count - index)).toISOString() }));
  mock.groups = groups.filter((_, index) => index !== 1);
  mock.recentlyAdded = groups.filter((_, index) => index !== 1);
  const reading = decodeScan(sampleBody("scan-feedback/reading_tags-admin"));
  if (reading.root_progress == null) throw new Error("读取标签样本缺少目录进度");
  const scan = mock.seedScan({ ...reading, root_progress: { ...reading.root_progress, name: longRootName } }, true);
  const report = decodeScanReport(sampleBody("scan-feedback/report-ready"));
  mock.scanReports.set(scan.id, {
    ...report,
    scan_run_id: scan.id,
    albums: { added: count, updated: 1234, missing: 5678 },
    tracks: { added: 1234567, updated: 7654321, missing: 9999999 },
    issues: { recorded: 100, may_be_truncated: true },
    added_albums: {
      items: groups.map((group, index) => ({
        group_id_at_scan: group.id,
        added_at: group.added_at,
        title_at_scan: group.representative.title,
        artist_at_scan: group.representative.artist,
        release_id: group.representative.id,
        current_group_id: index === 1 ? null : group.id,
        album: index === 1 ? null : group,
        claim_hints: index === 0 ? [{ id: "00000000-0000-4000-8000-000000000099", title: longAlbumTitle }] : [],
      })),
      pagination: { page: 1, page_size: 20, total: count },
    },
  });
  return scan;
}

/** 替换某次扫描的后端快照，保留 ID；终态从活动接口移除。 */
export function setScanFeedback(mock: ApiMock, scan: ScanDTO, sample: string, overrides: Partial<ScanDTO> = {}): ScanDTO {
  const snapshot = decodeScan(sampleBody(`scan-feedback/${sample}`));
  const next: ScanDTO = {
    ...snapshot,
    id: scan.id,
    scan_run_id: scan.id,
    root_progress: snapshot.root_progress == null ? null : {
      ...snapshot.root_progress,
      name: snapshot.root_progress.index === null ? null : longRootName,
    },
  };
  Object.assign(next, overrides);
  mock.scans.set(scan.id, next);
  mock.activeScanId = next.status === "running" ? next.id : null;
  return next;
}
