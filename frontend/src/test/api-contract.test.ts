import { describe, expect, it } from "vitest";
import { decodePlaybackFormats, decodeTrackLyrics, decodeTrackPlayback, decodeTrackPreparing } from "../features/player/api/decoders";
import { decodeLoggedOut, decodeSetupStatus, decodeViewer } from "../features/auth/api/decoders";
import {
  decodeReleaseComparison,
  decodeReleaseDetail,
  decodeReleaseEvidence,
  decodeReleaseGroupDetail,
  decodeReleaseGroupPage,
} from "../features/catalog/api/decoders";
import {
  decodeActiveScan,
  decodeDiagnosticsPage,
  decodeRootChanged,
  decodeRootCreated,
  decodeRootList,
  decodeRootOperationList,
  decodeScan,
  decodeScanPage,
} from "../features/library/api/decoders";
import {
  decodeCreatedMember,
  decodeMemberDisabledState,
  decodeMemberList,
  decodeSessionsRevoked,
} from "../features/members/api/decoders";
import {
  decodeCorrectionResult,
  decodeCorrectionState,
  decodeGroupChangeResult,
  decodeGroupDefaultResult,
  decodeOperationPage,
  decodeRevertResult,
} from "../features/governance/api/decoders";
import { decodePlaysCleared, decodeRecentReleases, decodeRecordedPlay } from "../features/listening/api/decoders";
import { decodeErrorEnvelope } from "../shared/api";
import { listApiSampleNames, loadApiSample } from "./api-samples";

// 契约测试：后端的每个真实响应样本都必须有一个 decoder 能解码，并且 decoder 保留了样本里的全部字段
// （往返后与样本相同）。后端新增字段时，样本测试先失败；在这里补上 decoder 才算确认前端已经知道这个字段。
type Contract = { status: number; decode: (input: unknown) => unknown };

const successContracts: Record<string, Contract> = {
  "playback/formats": { status: 200, decode: decodePlaybackFormats },
  "tracks/playback": { status: 200, decode: decodeTrackPlayback },
  "tracks/playback-derived": { status: 200, decode: decodeTrackPlayback },
  "tracks/playback-preparing": { status: 202, decode: decodeTrackPreparing },
  "tracks/lyrics-synced": { status: 200, decode: decodeTrackLyrics },
  "tracks/lyrics-plain": { status: 200, decode: decodeTrackLyrics },
  "tracks/lyrics-none": { status: 200, decode: decodeTrackLyrics },
  "auth/setup-status-required": { status: 200, decode: decodeSetupStatus },
  "auth/setup-status-completed": { status: 200, decode: decodeSetupStatus },
  "auth/setup-admin-created": { status: 201, decode: decodeViewer },
  "auth/login": { status: 200, decode: decodeViewer },
  "auth/me-admin": { status: 200, decode: decodeViewer },
  "auth/me-user": { status: 200, decode: decodeViewer },
  "auth/logout": { status: 200, decode: decodeLoggedOut },
  "users/list": { status: 200, decode: decodeMemberList },
  "users/create": { status: 201, decode: decodeCreatedMember },
  "users/disable": { status: 200, decode: decodeMemberDisabledState },
  "users/enable": { status: 200, decode: decodeMemberDisabledState },
  "users/revoke-sessions": { status: 200, decode: decodeSessionsRevoked },
  "library-roots/list-empty": { status: 200, decode: decodeRootList },
  "library-roots/list": { status: 200, decode: decodeRootList },
  "library-roots/create": { status: 201, decode: decodeRootCreated },
  "library-roots/create-existing-disabled": { status: 201, decode: decodeRootCreated },
  "library-roots/disable": { status: 200, decode: decodeRootChanged },
  "library-roots/restore": { status: 200, decode: decodeRootChanged },
  "library-root-operations/list-empty": { status: 200, decode: decodeRootOperationList },
  "library-root-operations/list": { status: 200, decode: decodeRootOperationList },
  "scans/active-none": { status: 200, decode: decodeActiveScan },
  "scans/active-running": { status: 200, decode: decodeActiveScan },
  "scans/list": { status: 200, decode: decodeScanPage },
  "scans/list-empty": { status: 200, decode: decodeScanPage },
  "scans/start-created": { status: 202, decode: decodeScan },
  "scans/start-existing": { status: 200, decode: decodeScan },
  "scans/get-running": { status: 200, decode: decodeScan },
  "scans/get-cancel-requested": { status: 200, decode: decodeScan },
  "scans/get-succeeded": { status: 200, decode: decodeScan },
  "scans/get-failed": { status: 200, decode: decodeScan },
  "scans/get-canceled": { status: 200, decode: decodeScan },
  "scans/get-incomplete": { status: 200, decode: decodeScan },
  "scans/cancel-requested": { status: 202, decode: decodeScan },
  "scans/cancel-already-finished": { status: 200, decode: decodeScan },
  "scans/diagnostics": { status: 200, decode: decodeDiagnosticsPage },
  "scans/diagnostics-empty": { status: 200, decode: decodeDiagnosticsPage },
  "scans/diagnostics-multiple-kinds": { status: 200, decode: decodeDiagnosticsPage },
  "release-groups/list-empty": { status: 200, decode: decodeReleaseGroupPage },
  "release-groups/list-one": { status: 200, decode: decodeReleaseGroupPage },
  "release-groups/list-one-with-artwork": { status: 200, decode: decodeReleaseGroupPage },
  "release-groups/get": { status: 200, decode: decodeReleaseGroupDetail },
  "release-groups/get-multiple": { status: 200, decode: decodeReleaseGroupDetail },
  "release-groups/get-default": { status: 200, decode: decodeReleaseGroupDetail },
  "release-groups/comparison": { status: 200, decode: decodeReleaseComparison },
  "releases/get": { status: 200, decode: decodeReleaseDetail },
  "releases/get-single": { status: 200, decode: decodeReleaseDetail },
  "releases/get-cue": { status: 200, decode: decodeReleaseDetail },
  "releases/get-corrected": { status: 200, decode: decodeReleaseDetail },
  "releases/evidence": { status: 200, decode: decodeReleaseEvidence },
  "corrections/get": { status: 200, decode: decodeCorrectionState },
  "corrections/get-corrected": { status: 200, decode: decodeCorrectionState },
  "corrections/patched": { status: 200, decode: decodeCorrectionResult },
  "group-changes/joined": { status: 200, decode: decodeGroupChangeResult },
  "group-changes/detached": { status: 200, decode: decodeGroupChangeResult },
  "group-defaults/set": { status: 200, decode: decodeGroupDefaultResult },
  "operations/list": { status: 200, decode: decodeOperationPage },
  "operations/list-by-release": { status: 200, decode: decodeOperationPage },
  "operations/reverted": { status: 200, decode: decodeRevertResult },
  "listening/recent-releases-empty": { status: 200, decode: decodeRecentReleases },
  "listening/recent-releases": { status: 200, decode: decodeRecentReleases },
  "listening/play-recorded": { status: 201, decode: decodeRecordedPlay },
  "listening/plays-cleared": { status: 200, decode: decodePlaysCleared },
};

// 错误样本都用统一信封；这里同时记录界面要处理的错误码与状态码。
const errorContracts: Record<string, { status: number; code: string }> = {
  "errors/root-disabled": { status: 409, code: "root_disabled" },
  "errors/not-directly-playable": { status: 409, code: "not_directly_playable" },
  "errors/source-unavailable": { status: 409, code: "source_unavailable" },
  "errors/source-changed": { status: 409, code: "source_changed" },
  "errors/derivation-not-ready": { status: 409, code: "derivation_not_ready" },
  "errors/derivation-failed": { status: 409, code: "derivation_failed" },
  "errors/derivation-busy": { status: 503, code: "derivation_busy" },
  "errors/not-found-track": { status: 404, code: "not_found" },
  "errors/play-too-short": { status: 400, code: "play_too_short" },
  "errors/range-not-satisfiable": { status: 416, code: "range_not_satisfiable" },
  "errors/unauthorized": { status: 401, code: "unauthorized" },
  "errors/forbidden": { status: 403, code: "forbidden" },
  "errors/origin-forbidden": { status: 403, code: "origin_forbidden" },
  "errors/invalid-credentials": { status: 401, code: "invalid_credentials" },
  "errors/setup-closed": { status: 409, code: "setup_closed" },
  "errors/invalid-input": { status: 400, code: "invalid_input" },
  "errors/invalid-input-missing-idempotency-key": { status: 400, code: "invalid_input" },
  "errors/username-taken": { status: 409, code: "username_taken" },
  "errors/last-admin": { status: 409, code: "last_admin" },
  "errors/invalid-library-path": { status: 400, code: "invalid_library_path" },
  "errors/revision-conflict": { status: 409, code: "revision_conflict" },
  "errors/revision-conflict-release": { status: 409, code: "revision_conflict" },
  "errors/revision-conflict-group": { status: 409, code: "revision_conflict" },
  "errors/not-in-group": { status: 409, code: "not_in_group" },
  "errors/invalid-correction": { status: 400, code: "invalid_correction" },
  "errors/no-changes": { status: 400, code: "no_changes" },
  "errors/same-group": { status: 409, code: "same_group" },
  "errors/already-alone": { status: 409, code: "already_alone" },
  "errors/group-not-found": { status: 404, code: "group_not_found" },
  "errors/already-reverted": { status: 409, code: "already_reverted" },
  "errors/not-revertible": { status: 409, code: "not_revertible" },
  "errors/revert-conflict": { status: 409, code: "revert_conflict" },
  "errors/not-found-operation": { status: 404, code: "not_found" },
  "errors/idempotency-conflict": { status: 409, code: "idempotency_conflict" },
  "errors/not-found-scan": { status: 404, code: "not_found" },
  "errors/not-found-api": { status: 404, code: "not_found" },
  "errors/invalid-id": { status: 400, code: "invalid_id" },
  "errors/invalid-id-user": { status: 400, code: "invalid_id" },
  "errors/invalid-pagination": { status: 400, code: "invalid_pagination" },
  "errors/not-found-user": { status: 404, code: "not_found" },
  "errors/not-found-release-group": { status: 404, code: "not_found" },
  "errors/not-found-release": { status: 404, code: "not_found" },
  "errors/method-not-allowed": { status: 405, code: "method_not_allowed" },
  "errors/database-unavailable": { status: 503, code: "database_unavailable" },
};

const sampleNames = listApiSampleNames();

describe("API 样本契约", () => {
  it("每个样本都有契约，每个契约都有样本", () => {
    const contractNames = [...Object.keys(successContracts), ...Object.keys(errorContracts)].sort();
    expect(sampleNames.filter((name) => !contractNames.includes(name)), "没有契约的样本").toEqual([]);
    expect(contractNames.filter((name) => !sampleNames.includes(name)), "没有样本的契约").toEqual([]);
  });

  it.each(sampleNames.filter((name) => name in successContracts))("成功样本 %s 被 decoder 完整解码", (name) => {
    const contract = successContracts[name];
    if (contract === undefined) throw new Error(`缺少契约：${name}`);
    const sample = loadApiSample(name);
    expect(sample.status).toBe(contract.status);
    expect(contract.decode(sample.body)).toEqual(sample.body);
  });

  it.each(sampleNames.filter((name) => name in errorContracts))("错误样本 %s 被错误信封 decoder 解码", (name) => {
    const contract = errorContracts[name];
    if (contract === undefined) throw new Error(`缺少契约：${name}`);
    const sample = loadApiSample(name);
    expect(sample.status).toBe(contract.status);
    const envelope = decodeErrorEnvelope(sample.body);
    expect(envelope).toEqual(sample.body);
    expect(envelope.error.code).toBe(contract.code);
    expect(envelope.request_id).toBe("sample-request");
  });
});
