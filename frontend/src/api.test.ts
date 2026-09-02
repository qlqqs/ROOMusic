import { describe, expect, it } from "vitest";
import {
  decodeApiErrorResponse,
  decodeActiveScan,
  decodeCreatedLibraryRoot,
  decodeLibraryRootList,
  decodeReleaseDetail,
  decodeReleaseList,
  decodeScanCancel,
  decodeScanStart,
  decodeScanStatus,
  decodeSession,
  decodeSetupStatus,
} from "./api";

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

  it("distinguishes accepted scans from persisted scan status", () => {
    const runningScan = { id: "scan-1", scan_run_id: "scan-1", status: "running", started_at: "2026-09-01T08:00:00Z", finished_at: null, cancel_requested_at: null };
    expect(decodeScanStart(runningScan)).toEqual(runningScan);
    expect(decodeScanStatus(runningScan)).toEqual({
      id: "scan-1",
      scan_run_id: "scan-1",
      status: "running",
      started_at: "2026-09-01T08:00:00Z",
      finished_at: null,
      cancel_requested_at: null,
    });
    const cancelRequested = { ...runningScan, cancel_requested_at: "2026-09-01T08:01:00Z" };
    expect(decodeScanCancel(cancelRequested)).toEqual(cancelRequested);
    expect(decodeActiveScan({ scan: cancelRequested })).toEqual({ scan: cancelRequested });
    expect(decodeActiveScan({ scan: null })).toEqual({ scan: null });
    expect(() => decodeScanStatus({ ...runningScan, status: "partially_done" })).toThrow();
    expect(() => decodeScanStatus({ ...runningScan, started_at: "not-a-date" })).toThrow();
    expect(() => decodeScanStatus({ ...runningScan, cancel_requested_at: undefined })).toThrow();
  });

  it("decodes paginated releases with stable Medium and Track identities", () => {
    const releaseList = decodeReleaseList({
      items: [{ id: "release-1", title: "专辑", artist: "艺术家" }],
      pagination: { page: 1, page_size: 50, total: 1 },
    });
    expect(releaseList.pagination.total).toBe(1);

    const releaseDetail = decodeReleaseDetail({
      id: "release-1",
      title: "专辑",
      artist: "艺术家",
      media: [{
        id: "medium-1",
        position: 1,
        title: "Medium",
        tracks: [{ id: "track-1", position: 1, title: "曲目", artist: "艺术家", source: "01.flac" }],
      }],
    });
    expect(releaseDetail.media[0]?.id).toBe("medium-1");
    expect(releaseDetail.media[0]?.tracks[0]?.id).toBe("track-1");
  });

  it("rejects missing pagination and unstable detail identities", () => {
    expect(() => decodeReleaseList({ items: [] })).toThrow();
    expect(() => decodeReleaseList({ items: [], pagination: { page: 0, page_size: 50, total: 0 } })).toThrow();
    expect(() => decodeReleaseDetail({ id: "release-1", title: "专辑", artist: "艺术家", media: [{ id: "", position: 1, title: "Medium", tracks: [] }] })).toThrow();
    expect(() => decodeReleaseDetail({ id: "release-1", title: "专辑", artist: "艺术家", media: [{ id: "medium-1", position: 1, title: "Medium", tracks: [{ position: 1, title: "曲目", artist: "艺术家", source: "01.flac" }] }] })).toThrow();
  });

  it("uses a safe fallback for malformed API errors", () => {
    const fallbackError = decodeApiErrorResponse("proxy failure", "req-fallback");
    expect(fallbackError.code).toBe("request_failed");
    expect(fallbackError.message).toBe("请求失败");
    expect(fallbackError.requestID).toBe("req-fallback");

    const classifiedError = decodeApiErrorResponse({ error: { code: "permission_denied", message: "无权访问" }, request_id: "req-1" }, null);
    expect(classifiedError.code).toBe("permission_denied");
    expect(classifiedError.requestID).toBe("req-1");
  });
});
