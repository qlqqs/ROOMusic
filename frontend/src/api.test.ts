import { describe, expect, it } from "vitest";
import {
  decodeActiveScan,
  decodeApiErrorResponse,
  decodeCreatedLibraryRoot,
  decodeLibraryRootList,
  decodeReleaseDetail,
  decodeReleaseEvidence,
  decodeReleaseList,
  decodeScanCancel,
  decodeScanDiagnostics,
  decodeScanStart,
  decodeScanStatus,
  decodeSession,
  decodeSetupStatus,
} from "./api";

const emptyDiagnosticSummary = { total: 0, counts: [] };
const releaseSummary = {
  id: "release-1",
  title: "专辑",
  artist: "艺术家",
  album_artist: "专辑艺术家",
  year: 2026,
  source_type: null,
  media_type: "CD",
  medium_count: 1,
  track_count: 1,
  attention_count: 1,
  artwork: null,
};
const releaseDetail = {
  ...releaseSummary,
  candidate_kind: "ordinary_directory",
  genre: null,
  catalog_number: "CAT-001",
  edition: "初回限定版",
  label: "示例唱片公司",
  barcode: "0123456789012",
  artwork: null,
  credits: [{ role: "album_artist", name: "专辑艺术家" }],
  evidence: [{ field: "title", source: "tag", confidence: "high", action: "auto_apply", rule_id: "majority_v1" }],
  media: [{
    id: "medium-1",
    position: 1,
    title: "Medium",
    tracks: [{
      id: "track-1",
      position: 1,
      title: "曲目",
      artist: "艺术家",
      source: "01.flac",
      source_kind: "flac_vorbis_comment",
      duration_seconds: 240.5,
      codec: "flac",
      bit_depth: 24,
      sample_rate: 96_000,
      channels: 2,
      bitrate: 2_400,
      cue: null,
      credits: [{ role: "composer", name: "作曲者" }],
    }],
  }],
};

describe("Core 0 API decoders", () => {
  it("decodes setup, session, and library-root contracts", () => {
    expect(decodeSetupStatus({ setup_required: true })).toEqual({ setup_required: true });
    expect(decodeSession({ username: "admin", role: "admin" })).toEqual({ username: "admin", role: "admin" });
    expect(decodeCreatedLibraryRoot({ id: "root-1", name: "Music", status: "active", revision: 1 })).toEqual({ id: "root-1", name: "Music", status: "active", revision: 1 });
    expect(decodeCreatedLibraryRoot({ id: "root-1", name: "Music", status: "disabled", revision: 2 })).toEqual({ id: "root-1", name: "Music", status: "disabled", revision: 2 });
    expect(decodeLibraryRootList({ items: [{ id: "root-1", path: "Music", status: "active", revision: 1, created_at: "2026-09-01T08:00:00Z", updated_at: "2026-09-01T08:00:00Z" }] })).toEqual({
      items: [{ id: "root-1", path: "Music", name: "Music", status: "active", revision: 1, created_at: "2026-09-01T08:00:00Z", updated_at: "2026-09-01T08:00:00Z" }],
    });
  });

  it("rejects malformed identity and root fields", () => {
    expect(() => decodeSetupStatus({ setup_required: "yes" })).toThrow();
    expect(() => decodeSession({ username: "" })).toThrow();
    expect(() => decodeSession({ username: "admin" })).toThrow();
    expect(() => decodeSession({ username: "admin", role: "owner" })).toThrow();
    expect(() => decodeLibraryRootList({ items: [{ id: "root-1", path: "Music" }] })).toThrow();
  });

  it("distinguishes scan terminal states and validates diagnostic aggregates", () => {
    const runningScan = { id: "scan-1", scan_run_id: "scan-1", status: "running", started_at: "2026-09-01T08:00:00Z", finished_at: null, cancel_requested_at: null, diagnostics: emptyDiagnosticSummary };
    expect(decodeScanStart(runningScan)).toEqual(runningScan);
    expect(decodeScanStatus(runningScan)).toEqual(runningScan);
    const cancelRequested = { ...runningScan, cancel_requested_at: "2026-09-01T08:01:00Z" };
    expect(decodeScanCancel(cancelRequested)).toEqual(cancelRequested);
    expect(decodeActiveScan({ scan: cancelRequested })).toEqual({ scan: cancelRequested });
    expect(decodeActiveScan({ scan: null })).toEqual({ scan: null });

    expect(decodeScanDiagnostics({
      summary: { total: 2, counts: [{ kind: "parse_failure", count: 2 }] },
      items: [{ id: "1", kind: "parse_failure", path: "Album/01.m4a", message: "音频文件解析失败" }],
    }).summary.total).toBe(2);
    expect(() => decodeScanStatus({ ...runningScan, status: "partially_done" })).toThrow();
    expect(() => decodeScanStatus({ ...runningScan, diagnostics: undefined })).toThrow();
    expect(() => decodeScanStatus({ ...runningScan, started_at: "2026" })).toThrow();
    expect(() => decodeScanDiagnostics({ summary: { total: 2, counts: [{ kind: "parse_failure", count: 1 }] }, items: [] })).toThrow();
    expect(() => decodeScanDiagnostics({ summary: { total: 1, counts: [{ kind: "parse_failure", count: 1 }] }, items: [{ id: "1", kind: "parse_failure", path: "/srv/private.flac", message: "失败" }] })).toThrow();
  });

  it("decodes release counts, nullable facts, audio facts, and evidence summaries", () => {
    const releaseList = decodeReleaseList({
      items: [releaseSummary],
      pagination: { page: 1, page_size: 50, total: 1 },
    });
    expect(releaseList.items[0]?.attention_count).toBe(1);
    expect(releaseList.items[0]?.source_type).toBeNull();

    const decodedDetail = decodeReleaseDetail(releaseDetail);
    expect(decodedDetail.media[0]?.id).toBe("medium-1");
    expect(decodedDetail.media[0]?.tracks[0]?.bit_depth).toBe(24);
    expect(decodedDetail.media[0]?.tracks[0]?.credits[0]?.role).toBe("composer");
    expect(decodedDetail.credits[0]?.role).toBe("album_artist");
    expect(decodedDetail.edition).toBe("初回限定版");
    expect(decodedDetail.label).toBe("示例唱片公司");
    expect(decodedDetail.barcode).toBe("0123456789012");
  });

  it("strictly decodes the additive summary artwork field", () => {
    const artwork = { resource_id: "cover-abc123", mime: "image/jpeg", width: 600, height: 600 };
    const withArtwork = decodeReleaseList({ items: [{ ...releaseSummary, artwork }], pagination: { page: 1, page_size: 50, total: 1 } });
    expect(withArtwork.items[0]?.artwork).toEqual(artwork);

    const withoutArtwork = decodeReleaseList({ items: [releaseSummary], pagination: { page: 1, page_size: 50, total: 1 } });
    expect(withoutArtwork.items[0]?.artwork).toBeNull();

    const detailWithArtwork = decodeReleaseDetail({ ...releaseDetail, artwork });
    expect(detailWithArtwork.artwork).toEqual(artwork);

    const missingArtwork = { ...releaseSummary } as Record<string, unknown>;
    delete missingArtwork.artwork;
    expect(() => decodeReleaseList({ items: [missingArtwork], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, mime: "image/svg+xml" } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, width: 0 } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, height: -3 } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, height: 1.5 } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, resource_id: "x".repeat(256) } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, resource_id: "../cover" } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, resource_id: "a/b" } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, resource_id: ".." } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
    expect(() => decodeReleaseList({ items: [{ ...releaseSummary, artwork: { ...artwork, resource_id: `cover${String.fromCharCode(7)}id` } }], pagination: { page: 1, page_size: 50, total: 1 } })).toThrow();
  });

  it("strictly decodes bounded administrator evidence", () => {
    const rawEvidence = {
      release_id: "release-1",
      fields: [{
        field: "title",
        value: "专辑",
        source: "tag",
        confidence: "medium",
        action: "uncertain_apply",
        rule_id: "majority_v1",
        candidates: ["专辑", "专辑（重制版）"],
        reason_code: "inconsistent_candidates",
      }],
      grouping: {
        candidate_kind: "same_dir_split",
        rule_id: "organizer_v2",
        source_refs: ["Album/01.flac"],
        reason_codes: ["title:inconsistent_candidates"],
      },
      truncated: false,
    };
    const evidence = decodeReleaseEvidence(rawEvidence);
    expect(evidence.grouping?.source_refs).toEqual(["Album/01.flac"]);
    expect(evidence.fields[0]?.action).toBe("uncertain_apply");

    expect(() => decodeReleaseEvidence({ ...rawEvidence, grouping: { ...rawEvidence.grouping, source_refs: ["/srv/music/private.flac"] } })).toThrow();
    expect(() => decodeReleaseEvidence({ ...rawEvidence, grouping: { ...rawEvidence.grouping, source_refs: ["file:///srv/music/private.flac"] } })).toThrow();
    expect(() => decodeReleaseEvidence({ ...rawEvidence, fields: [{ ...rawEvidence.fields[0], confidence: "certain" }] })).toThrow();
    expect(() => decodeReleaseEvidence({ ...rawEvidence, fields: [{ ...rawEvidence.fields[0], candidates: Array.from({ length: 21 }, (_, index) => `候选 ${index}`) }] })).toThrow();
  });

  it("rejects missing fields, unknown enums, and unbounded arrays", () => {
    expect(() => decodeReleaseList({ items: [], pagination: { page: 0, page_size: 50, total: 0 } })).toThrow();
    expect(() => decodeReleaseList({ items: [], pagination: { page: 1, page_size: 101, total: 0 } })).toThrow();
    expect(() => decodeReleaseList({ items: Array.from({ length: 101 }, () => releaseSummary), pagination: { page: 1, page_size: 100, total: 101 } })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, candidate_kind: "guessed" })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, evidence: [{ ...releaseDetail.evidence[0], action: "confirmed" }] })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, media: [{ ...releaseDetail.media[0], id: "" }] })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, media: [{ ...releaseDetail.media[0], tracks: [{ ...releaseDetail.media[0].tracks[0], id: undefined }] }] })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, edition: undefined })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, media: [{ ...releaseDetail.media[0], tracks: [{ ...releaseDetail.media[0].tracks[0], credits: undefined }] }] })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, media: [{ ...releaseDetail.media[0], tracks: [{ ...releaseDetail.media[0].tracks[0], credits: [{ role: "arranger", name: "编曲者" }] }] }] })).toThrow();
    expect(() => decodeReleaseDetail({ ...releaseDetail, media: [{ ...releaseDetail.media[0], tracks: [{ ...releaseDetail.media[0].tracks[0], credits: Array.from({ length: 101 }, (_, index) => ({ role: "composer", name: `作曲者 ${index}` })) }] }] })).toThrow();
    const hundredCredits = Array.from({ length: 100 }, (_, index) => ({ role: "composer", name: `作曲者 ${index}` }));
    expect(() => decodeReleaseDetail({
      ...releaseDetail,
      media: [{
        ...releaseDetail.media[0],
        tracks: Array.from({ length: 101 }, (_, trackIndex) => ({
          ...releaseDetail.media[0].tracks[0],
          id: `credited-track-${trackIndex}`,
          credits: hundredCredits,
        })),
      }],
    })).toThrow();
    const boundaryTracks = Array.from({ length: 10_000 }, (_, trackIndex) => ({
      ...releaseDetail.media[0].tracks[0],
      id: `track-${trackIndex}`,
    }));
    const boundaryMedium = {
      ...releaseDetail.media[0],
      tracks: boundaryTracks,
    };
    expect(decodeReleaseDetail({
      ...releaseDetail,
      media: [boundaryMedium],
    }).media[0]?.tracks).toHaveLength(10_000);
    expect(() => decodeReleaseDetail({
      ...releaseDetail,
      media: [boundaryMedium, {
        ...releaseDetail.media[0],
        id: "medium-overflow",
        tracks: [{
          ...releaseDetail.media[0].tracks[0],
          id: "track-overflow",
        }],
      }],
    })).toThrow();
  });

  it("uses a safe fallback for malformed API errors", () => {
    const fallbackError = decodeApiErrorResponse("proxy failure", "req-fallback");
    expect(fallbackError.code).toBe("request_failed");
    expect(fallbackError.message).toBe("请求失败");
    expect(fallbackError.requestID).toBe("req-fallback");

    const classifiedError = decodeApiErrorResponse({ error: { code: "permission_denied", message: "无权访问" }, request_id: "req-1" }, null);
    expect(classifiedError.code).toBe("permission_denied");
    expect(classifiedError.requestID).toBe("req-1");

    const oversizedError = decodeApiErrorResponse({ error: { code: "x".repeat(4_097), message: "x".repeat(4_097) }, request_id: "x".repeat(4_097) }, "req-fallback");
    expect(oversizedError.code).toBe("request_failed");
    expect(oversizedError.message).toBe("请求失败");
    expect(oversizedError.requestID).toBe("req-fallback");
  });
});
