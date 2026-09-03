export type SetupStatusDTO = { setup_required: boolean };
export type SessionDTO = { username: string; role: "admin" | "user" };
export type UserDTO = { id: string; username: string; role: "admin" | "user"; disabled: boolean; created_at: string };
export type CreatedUserDTO = { id: string; username: string; role: "user" };
export type UpdatedUserDTO = { disabled: boolean };
export type RootOperationDTO = { id: string; root_id?: string; operation: string; status: string; revision?: number; error_code?: string; created_at: string };
export type LibraryRootDTO = { id: string; path: string; name?: string; status: "active" | "disabled"; revision: number; created_at: string; updated_at: string };
export type CreatedLibraryRootDTO = { id: string; name: string; status: "active" | "disabled"; revision: number };
export type UpdatedLibraryRootDTO = { id: string; path: string; status: "active" | "disabled"; revision: number };

const scanStatuses = ["running", "succeeded", "failed", "canceled", "incomplete"] as const;
export type ScanStatusValue = (typeof scanStatuses)[number];
export type ScanDiagnosticCountDTO = { kind: string; count: number };
export type ScanDiagnosticsSummaryDTO = { total: number; counts: ScanDiagnosticCountDTO[] };
export type ScanDiagnosticDTO = { id: string; kind: string; path: string | null; message: string };
export type ScanDiagnosticsDTO = { summary: ScanDiagnosticsSummaryDTO; items: ScanDiagnosticDTO[] };
export type ScanStatusDTO = {
  id: string;
  scan_run_id: string;
  status: ScanStatusValue;
  started_at: string;
  finished_at: string | null;
  cancel_requested_at: string | null;
  diagnostics: ScanDiagnosticsSummaryDTO;
};
export type ActiveScanDTO = { scan: ScanStatusDTO | null };

const candidateKinds = ["ordinary_directory", "strict_multidisc", "box_leaf", "same_dir_split", "loose_album", "loose_unknown", "legacy"] as const;
const evidenceConfidences = ["high", "medium", "low"] as const;
const evidenceActions = ["auto_apply", "uncertain_apply"] as const;
const artworkMIMETypes = ["image/jpeg", "image/png", "image/gif", "image/webp"] as const;

export type CandidateKind = (typeof candidateKinds)[number];
export type EvidenceConfidence = (typeof evidenceConfidences)[number];
export type EvidenceAction = (typeof evidenceActions)[number];
export type ReleaseSummaryDTO = {
  id: string;
  title: string;
  artist: string;
  album_artist: string | null;
  year: number | null;
  source_type: string | null;
  media_type: string | null;
  medium_count: number;
  track_count: number;
  attention_count: number;
};
export type PaginationDTO = { page: number; page_size: number; total: number };
export type ReleaseListDTO = { items: ReleaseSummaryDTO[]; pagination: PaginationDTO };
export type CueTrackDTO = { index_frames: number | null; end_frames: number | null; start_seconds: number | null; end_seconds: number | null; isrc: string | null };
export type TrackDTO = {
  id: string;
  title: string;
  artist: string;
  position: number;
  source: string;
  source_kind: string;
  duration_seconds: number | null;
  codec: string | null;
  bit_depth: number | null;
  sample_rate: number | null;
  channels: number | null;
  bitrate: number | null;
  cue: CueTrackDTO | null;
};
export type MediumDTO = { id: string; position: number; title: string; tracks: TrackDTO[] };
export type ReleaseArtworkDTO = { resource_id: string; mime: (typeof artworkMIMETypes)[number]; width: number; height: number };
export type ReleaseCreditDTO = { role: string; name: string };
export type ReleaseEvidenceSummaryDTO = { field: string; source: string; confidence: EvidenceConfidence; action: EvidenceAction; rule_id: string };
export type ReleaseDetailDTO = ReleaseSummaryDTO & {
  candidate_kind: CandidateKind;
  genre: string | null;
  catalog_number: string | null;
  media: MediumDTO[];
  credits: ReleaseCreditDTO[];
  artwork: ReleaseArtworkDTO | null;
  evidence: ReleaseEvidenceSummaryDTO[];
};
export type ReleaseFieldEvidenceDTO = ReleaseEvidenceSummaryDTO & { value: string | null; candidates: string[]; reason_code: string | null };
export type ReleaseGroupingEvidenceDTO = { candidate_kind: CandidateKind; rule_id: string; source_refs: string[]; reason_codes: string[] };
export type ReleaseEvidenceDTO = { release_id: string; fields: ReleaseFieldEvidenceDTO[]; grouping: ReleaseGroupingEvidenceDTO | null; truncated: boolean };

const maxReleaseItems = 100;
const maxMediaItems = 256;
const maxTrackItems = 10_000;
const maxEvidenceItems = 100;
const maxEvidenceCandidates = 20;
const maxEvidenceSourceRefs = 100;
const maxDiagnosticItems = 100;
const maxWireStringLength = 4_096;

type Decoder<ResponseDTO> = (input: unknown) => ResponseDTO;
type UnknownObject = { [key: string]: unknown };

export class ApiRequestError extends Error {
  readonly code: string;
  readonly requestID: string | null;

  constructor(code: string, message: string, requestID: string | null) {
    super(message);
    this.name = "ApiRequestError";
    this.code = code;
    this.requestID = requestID;
  }
}

export async function requestApi<ResponseDTO>(path: string, decoder: Decoder<ResponseDTO>, options?: RequestInit): Promise<ResponseDTO> {
  const response = await fetch(path, {
    ...options,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...options?.headers },
  });
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new ApiRequestError("invalid_response", "服务器返回了无法识别的响应", response.headers.get("X-Request-ID"));
  }
  if (!response.ok) {
    throw decodeApiErrorResponse(payload, response.headers.get("X-Request-ID"));
  }
  try {
    return decoder(payload);
  } catch {
    throw new ApiRequestError("invalid_response", "服务器返回的数据格式无效", response.headers.get("X-Request-ID"));
  }
}

export function decodeSetupStatus(input: unknown): SetupStatusDTO {
  const object = requireObject(input, "setup status");
  return { setup_required: requireBoolean(object.setup_required, "setup_required") };
}

export function decodeSession(input: unknown): SessionDTO {
  const object = requireObject(input, "session");
  const roleValue = requireNonEmptyString(object.role, "role");
  if (roleValue !== "admin" && roleValue !== "user") throw new Error("unknown role");
  const role: SessionDTO["role"] = roleValue;
  return { username: requireNonEmptyString(object.username, "username"), role };
}
export function decodeCreatedUser(input: unknown): CreatedUserDTO {
  const object = requireObject(input, "created user");
  if (object.role !== "user") throw new Error("created user role must be user");
  return { id: requireNonEmptyString(object.id, "id"), username: requireNonEmptyString(object.username, "username"), role: "user" };
}
export function decodeUpdatedUser(input: unknown): UpdatedUserDTO {
  const object = requireObject(input, "updated user");
  return { disabled: requireBoolean(object.disabled, "disabled") };
}
export function decodeUserList(input: unknown): {items: UserDTO[]} { const object=requireObject(input,"user list"); return {items: requireArray(object.items,"items").map(item=>{const u=requireObject(item,"user"); const role=requireNonEmptyString(u.role,"role"); if(role!=="admin"&&role!=="user") throw new Error("unknown role"); return {id:requireNonEmptyString(u.id,"id"),username:requireNonEmptyString(u.username,"username"),role,disabled:requireBoolean(u.disabled,"disabled"),created_at:requireTimestamp(u.created_at,"created_at")};})}; }

export function decodeRootOperationList(input: unknown): { items: RootOperationDTO[] } {
  const object = requireObject(input, "root operation list");
  return { items: requireArray(object.items, "items").map((item) => {
    const operation = requireObject(item, "root operation");
    return {
      id: requireNonEmptyString(operation.id, "id"),
      root_id: operation.root_id === undefined ? undefined : requireNonEmptyString(operation.root_id, "root_id"),
      operation: requireNonEmptyString(operation.operation, "operation"),
      status: requireNonEmptyString(operation.status, "status"),
      revision: operation.revision === undefined ? undefined : requireSafeInteger(operation.revision, "revision", 1),
      error_code: operation.error_code === undefined ? undefined : requireNonEmptyString(operation.error_code, "error_code"),
      created_at: requireTimestamp(operation.created_at, "created_at"),
    };
  }) };
}

export function decodeLibraryRootList(input: unknown): { items: LibraryRootDTO[] } {
  const object = requireObject(input, "library root list");
  return {
    items: requireArray(object.items, "items").map((item) => {
      const root = requireObject(item, "library root");
      const status = requireNonEmptyString(root.status, "status");
      if (status !== "active" && status !== "disabled") throw new Error("unknown root status");
      return {
        id: requireNonEmptyString(root.id, "id"),
        path: requireNonEmptyString(root.path, "path"),
        name: requireNonEmptyString(root.name ?? root.path, "name"),
        status,
        revision: requireSafeInteger(root.revision, "revision", 1),
        created_at: requireTimestamp(root.created_at, "created_at"),
        updated_at: requireTimestamp(root.updated_at, "updated_at"),
      };
    }),
  };
}

export function decodeCreatedLibraryRoot(input: unknown): CreatedLibraryRootDTO {
  const object = requireObject(input, "created library root");
  const status = requireNonEmptyString(object.status, "status");
  if (status !== "active" && status !== "disabled") throw new Error("unknown root status");
  return { id: requireNonEmptyString(object.id, "id"), name: requireNonEmptyString(object.name, "name"), status, revision: requireSafeInteger(object.revision, "revision", 1) };
}

export function decodeUpdatedLibraryRoot(input: unknown): UpdatedLibraryRootDTO {
  const object = requireObject(input, "updated library root");
  const status = requireNonEmptyString(object.status, "status");
  if (status !== "active" && status !== "disabled") throw new Error("unknown root status");
  return { id: requireNonEmptyString(object.id, "id"), path: requireNonEmptyString(object.path, "path"), status, revision: requireSafeInteger(object.revision, "revision", 1) };
}

export const decodeScanStart = decodeScanStatus;
export const decodeScanCancel = decodeScanStatus;

export function decodeScanStatus(input: unknown): ScanStatusDTO {
  const object = requireObject(input, "scan status");
  const status = requireNonEmptyString(object.status, "status");
  if (!isScanStatus(status)) {
    throw new Error("unknown scan status");
  }
  return {
    id: requireNonEmptyString(object.id, "id"),
    scan_run_id: requireNonEmptyString(object.scan_run_id, "scan_run_id"),
    status,
    started_at: requireTimestamp(object.started_at, "started_at"),
    finished_at: object.finished_at === null ? null : requireTimestamp(object.finished_at, "finished_at"),
    cancel_requested_at: object.cancel_requested_at === null ? null : requireTimestamp(object.cancel_requested_at, "cancel_requested_at"),
    diagnostics: decodeScanDiagnosticsSummary(object.diagnostics),
  };
}

export function decodeActiveScan(input: unknown): ActiveScanDTO {
  const object = requireObject(input, "active scan");
  return { scan: object.scan === null ? null : decodeScanStatus(object.scan) };
}

function isScanStatus(value: string): value is ScanStatusValue {
  return scanStatuses.some((knownStatus) => knownStatus === value);
}

export function decodeScanDiagnostics(input: unknown): ScanDiagnosticsDTO {
  const object = requireObject(input, "scan diagnostics");
  return {
    summary: decodeScanDiagnosticsSummary(object.summary),
    items: requireBoundedArray(object.items, "diagnostic items", maxDiagnosticItems).map((item) => {
      const diagnostic = requireObject(item, "diagnostic");
      return {
        id: requireNonEmptyString(diagnostic.id, "diagnostic.id"),
        kind: requireBoundedString(diagnostic.kind, "diagnostic.kind"),
        path: diagnostic.path === null ? null : requireSafeRelativePath(diagnostic.path, "diagnostic.path"),
        message: requireBoundedString(diagnostic.message, "diagnostic.message"),
      };
    }),
  };
}

function decodeScanDiagnosticsSummary(input: unknown): ScanDiagnosticsSummaryDTO {
  const object = requireObject(input, "diagnostic summary");
  const counts = requireBoundedArray(object.counts, "diagnostic counts", maxDiagnosticItems).map((item) => {
    const count = requireObject(item, "diagnostic count");
    return {
      kind: requireBoundedString(count.kind, "diagnostic count.kind"),
      count: requireNonNegativeInteger(count.count, "diagnostic count.count", 0),
    };
  });
  const total = requireNonNegativeInteger(object.total, "diagnostic summary.total", 0);
  if (counts.reduce((sum, item) => sum + item.count, 0) !== total) {
    throw new Error("diagnostic counts do not match total");
  }
  return { total, counts };
}

export function decodeReleaseList(input: unknown): ReleaseListDTO {
  const object = requireObject(input, "release list");
  const pagination = requireObject(object.pagination, "pagination");
  const items = requireBoundedArray(object.items, "items", maxReleaseItems).map(decodeReleaseSummary);
  const pageSize = requireNonNegativeInteger(pagination.page_size, "page_size", 1);
  if (pageSize > maxReleaseItems || items.length > pageSize) {
    throw new Error("release pagination exceeds its maximum length");
  }
  return {
    items,
    pagination: {
      page: requireNonNegativeInteger(pagination.page, "page", 1),
      page_size: pageSize,
      total: requireNonNegativeInteger(pagination.total, "total", 0),
    },
  };
}

export function decodeReleaseDetail(input: unknown): ReleaseDetailDTO {
  const object = requireObject(input, "release detail");
  const summary = decodeReleaseSummary(object);
  const artworkObject = object.artwork === null ? null : requireObject(object.artwork, "artwork");
  let decodedTrackCount = 0;
  const media = requireBoundedArray(object.media, "media", maxMediaItems).map((item) => {
    const medium = requireObject(item, "medium");
    const trackItems = requireBoundedArray(medium.tracks, "medium.tracks", maxTrackItems);
    if (decodedTrackCount + trackItems.length > maxTrackItems) {
      throw new Error("release tracks exceed their maximum length");
    }
    decodedTrackCount += trackItems.length;
    const tracks = trackItems.map((trackItem) => {
      const track = requireObject(trackItem, "track");
      const cueObject = track.cue === null ? null : requireObject(track.cue, "track.cue");
      return {
        id: requireNonEmptyString(track.id, "track.id"),
        title: requireBoundedString(track.title, "track.title"),
        artist: requireBoundedString(track.artist, "track.artist"),
        position: requireNonNegativeInteger(track.position, "track.position", 1),
        source: requireNonEmptyString(track.source, "track.source"),
        source_kind: requireNonEmptyString(track.source_kind, "track.source_kind"),
        duration_seconds: requireNullableNonNegativeNumber(track.duration_seconds, "track.duration_seconds"),
        codec: requireNullableNonEmptyString(track.codec, "track.codec"),
        bit_depth: requireNullableNonNegativeInteger(track.bit_depth, "track.bit_depth", 1),
        sample_rate: requireNullableNonNegativeInteger(track.sample_rate, "track.sample_rate", 1),
        channels: requireNullableNonNegativeInteger(track.channels, "track.channels", 1),
        bitrate: requireNullableNonNegativeInteger(track.bitrate, "track.bitrate", 1),
        cue: cueObject ? {
          index_frames: requireNullableNonNegativeInteger(cueObject.index_frames, "track.cue.index_frames", 0),
          end_frames: requireNullableNonNegativeInteger(cueObject.end_frames, "track.cue.end_frames", 0),
          start_seconds: requireNullableNonNegativeNumber(cueObject.start_seconds, "track.cue.start_seconds"),
          end_seconds: requireNullableNonNegativeNumber(cueObject.end_seconds, "track.cue.end_seconds"),
          isrc: requireNullableNonEmptyString(cueObject.isrc, "track.cue.isrc"),
        } : null,
      };
    });
    return {
      id: requireNonEmptyString(medium.id, "medium.id"),
      position: requireNonNegativeInteger(medium.position, "medium.position", 1),
      title: requireBoundedString(medium.title, "medium.title"),
      tracks,
    };
  });
  return {
    ...summary,
    candidate_kind: requireEnum(object.candidate_kind, "candidate_kind", candidateKinds),
    genre: requireNullableNonEmptyString(object.genre, "genre"),
    catalog_number: requireNullableNonEmptyString(object.catalog_number, "catalog_number"),
    artwork: artworkObject ? {
      resource_id: requireNonEmptyString(artworkObject.resource_id, "artwork.resource_id"),
      mime: requireEnum(artworkObject.mime, "artwork.mime", artworkMIMETypes),
      width: requireNonNegativeInteger(artworkObject.width, "artwork.width", 1),
      height: requireNonNegativeInteger(artworkObject.height, "artwork.height", 1),
    } : null,
    media,
    credits: requireBoundedArray(object.credits, "credits", maxEvidenceItems).map((item) => {
      const credit = requireObject(item, "credit");
      return { role: requireNonEmptyString(credit.role, "credit.role"), name: requireNonEmptyString(credit.name, "credit.name") };
    }),
    evidence: requireBoundedArray(object.evidence, "evidence", maxEvidenceItems).map(decodeReleaseEvidenceSummary),
  };
}

export function decodeReleaseEvidence(input: unknown): ReleaseEvidenceDTO {
  const object = requireObject(input, "release evidence");
  const groupingObject = object.grouping === null ? null : requireObject(object.grouping, "grouping evidence");
  return {
    release_id: requireNonEmptyString(object.release_id, "release_id"),
    fields: requireBoundedArray(object.fields, "evidence fields", maxEvidenceItems).map((item) => {
      const field = requireObject(item, "field evidence");
      return {
        ...decodeReleaseEvidenceSummary(field),
        value: requireNullableNonEmptyString(field.value, "field evidence.value"),
        candidates: requireBoundedArray(field.candidates, "field evidence.candidates", maxEvidenceCandidates).map((candidate) => requireNonEmptyString(candidate, "field evidence.candidate")),
        reason_code: requireNullableNonEmptyString(field.reason_code, "field evidence.reason_code"),
      };
    }),
    grouping: groupingObject ? {
      candidate_kind: requireEnum(groupingObject.candidate_kind, "grouping candidate_kind", candidateKinds),
      rule_id: requireNonEmptyString(groupingObject.rule_id, "grouping rule_id"),
      source_refs: requireBoundedArray(groupingObject.source_refs, "grouping source_refs", maxEvidenceSourceRefs).map((sourceRef) => requireSafeRelativePath(sourceRef, "grouping source_ref")),
      reason_codes: requireBoundedArray(groupingObject.reason_codes, "grouping reason_codes", maxEvidenceCandidates).map((reasonCode) => requireNonEmptyString(reasonCode, "grouping reason_code")),
    } : null,
    truncated: requireBoolean(object.truncated, "truncated"),
  };
}

export function decodeAcknowledgement(input: unknown): UnknownObject {
  return requireObject(input, "acknowledgement");
}

function decodeReleaseSummary(input: unknown): ReleaseSummaryDTO {
  const object = requireObject(input, "release summary");
  return {
    id: requireNonEmptyString(object.id, "release.id"),
    title: requireBoundedString(object.title, "release.title"),
    artist: requireBoundedString(object.artist, "release.artist"),
    album_artist: requireNullableNonEmptyString(object.album_artist, "release.album_artist"),
    year: requireNullableNonNegativeInteger(object.year, "release.year", 1),
    source_type: requireNullableNonEmptyString(object.source_type, "release.source_type"),
    media_type: requireNullableNonEmptyString(object.media_type, "release.media_type"),
    medium_count: requireNonNegativeInteger(object.medium_count, "release.medium_count", 0),
    track_count: requireNonNegativeInteger(object.track_count, "release.track_count", 0),
    attention_count: requireNonNegativeInteger(object.attention_count, "release.attention_count", 0),
  };
}

function decodeReleaseEvidenceSummary(input: unknown): ReleaseEvidenceSummaryDTO {
  const evidence = requireObject(input, "evidence summary");
  return {
    field: requireNonEmptyString(evidence.field, "evidence.field"),
    source: requireNonEmptyString(evidence.source, "evidence.source"),
    confidence: requireEnum(evidence.confidence, "evidence.confidence", evidenceConfidences),
    action: requireEnum(evidence.action, "evidence.action", evidenceActions),
    rule_id: requireNonEmptyString(evidence.rule_id, "evidence.rule_id"),
  };
}

export function decodeApiErrorResponse(input: unknown, fallbackRequestID: string | null): ApiRequestError {
  const boundedFallbackRequestID = optionalBoundedString(fallbackRequestID);
  if (!isObject(input)) {
    return new ApiRequestError("request_failed", "请求失败", boundedFallbackRequestID);
  }
  const errorDetail = isObject(input.error) ? input.error : null;
  const code = optionalBoundedString(errorDetail?.code) ?? "request_failed";
  const message = optionalBoundedString(errorDetail?.message) ?? "请求失败";
  const requestID = optionalBoundedString(input.request_id) ?? boundedFallbackRequestID;
  return new ApiRequestError(code, message, requestID);
}

function optionalBoundedString(input: unknown): string | null {
  return typeof input === "string" && input.trim() !== "" && input.length <= maxWireStringLength ? input : null;
}

function requireObject(input: unknown, fieldName: string): UnknownObject {
  if (!isObject(input)) {
    throw new Error(`${fieldName} must be an object`);
  }
  return input;
}

function isObject(input: unknown): input is UnknownObject {
  return typeof input === "object" && input !== null && !Array.isArray(input);
}

function requireArray(input: unknown, fieldName: string): unknown[] {
  if (!Array.isArray(input)) {
    throw new Error(`${fieldName} must be an array`);
  }
  return input;
}

function requireBoundedArray(input: unknown, fieldName: string, maximumLength: number): unknown[] {
  const result = requireArray(input, fieldName);
  if (result.length > maximumLength) {
    throw new Error(`${fieldName} exceeds its maximum length`);
  }
  return result;
}

function requireNonEmptyString(input: unknown, fieldName: string): string {
  const value = requireBoundedString(input, fieldName);
  if (value.trim() === "") {
    throw new Error(`${fieldName} must be a non-empty string`);
  }
  return value;
}

function requireBoundedString(input: unknown, fieldName: string): string {
  if (typeof input !== "string" || input.length > maxWireStringLength) {
    throw new Error(`${fieldName} must be a bounded string`);
  }
  return input;
}

function requireNullableNonEmptyString(input: unknown, fieldName: string): string | null {
  return input === null ? null : requireNonEmptyString(input, fieldName);
}

function requireEnum<const Values extends readonly string[]>(input: unknown, fieldName: string, values: Values): Values[number] {
  const value = requireNonEmptyString(input, fieldName);
  if (!values.some((candidate) => candidate === value)) {
    throw new Error(`${fieldName} contains an unknown value`);
  }
  return value as Values[number];
}

function requireBoolean(input: unknown, fieldName: string): boolean {
  if (typeof input !== "boolean") {
    throw new Error(`${fieldName} must be a boolean`);
  }
  return input;
}

function requireTimestamp(input: unknown, fieldName: string): string {
  const timestamp = requireNonEmptyString(input, fieldName);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(timestamp) || Number.isNaN(Date.parse(timestamp))) {
    throw new Error(`${fieldName} must be a timestamp`);
  }
  return timestamp;
}

function requireNonNegativeInteger(input: unknown, fieldName: string, minimum: number): number {
  if (typeof input !== "number" || !Number.isSafeInteger(input) || input < minimum) {
    throw new Error(`${fieldName} must be a safe integer greater than or equal to ${minimum}`);
  }
  return input;
}

function requireNonNegativeNumber(input: unknown, fieldName: string): number {
  if (typeof input !== "number" || !Number.isFinite(input) || input < 0) throw new Error(`${fieldName} must be a non-negative number`);
  return input;
}

function requireNullableNonNegativeInteger(input: unknown, fieldName: string, minimum: number): number | null {
  return input === null ? null : requireNonNegativeInteger(input, fieldName, minimum);
}

function requireNullableNonNegativeNumber(input: unknown, fieldName: string): number | null {
  return input === null ? null : requireNonNegativeNumber(input, fieldName);
}

function requireSafeRelativePath(input: unknown, fieldName: string): string {
  const value = requireNonEmptyString(input, fieldName).replaceAll("\\", "/");
  if (value.startsWith("/") || value.split("/", 1)[0]?.includes(":")) {
    throw new Error(`${fieldName} must be relative`);
  }
  const segments = value.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new Error(`${fieldName} must be a normalized relative path`);
  }
  return value;
}

const requireSafeInteger = requireNonNegativeInteger;
