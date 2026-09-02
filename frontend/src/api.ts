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
export type ScanStatusDTO = {
  id: string;
  scan_run_id: string;
  status: ScanStatusValue;
  started_at: string;
  finished_at: string | null;
  cancel_requested_at: string | null;
};
export type ActiveScanDTO = { scan: ScanStatusDTO | null };

export type ReleaseSummaryDTO = { id: string; title: string; artist: string; year?: number };
export type PaginationDTO = { page: number; page_size: number; total: number };
export type ReleaseListDTO = { items: ReleaseSummaryDTO[]; pagination: PaginationDTO };
export type TrackDTO = { id: string; title: string; artist: string; position: number; source: string };
export type MediumDTO = { id: string; position: number; title: string; tracks: TrackDTO[] };
export type ReleaseArtworkDTO = { resource_id: string; mime: string; width: number; height: number };
export type ReleaseDetailDTO = ReleaseSummaryDTO & { media: MediumDTO[]; artwork?: ReleaseArtworkDTO };

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
    credentials: "include",
    ...options,
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
  };
}

export function decodeActiveScan(input: unknown): ActiveScanDTO {
  const object = requireObject(input, "active scan");
  return { scan: object.scan === null ? null : decodeScanStatus(object.scan) };
}

function isScanStatus(value: string): value is ScanStatusValue {
  return scanStatuses.some((knownStatus) => knownStatus === value);
}

export function decodeReleaseList(input: unknown): ReleaseListDTO {
  const object = requireObject(input, "release list");
  const pagination = requireObject(object.pagination, "pagination");
  return {
    items: requireArray(object.items, "items").map(decodeReleaseSummary),
    pagination: {
      page: requireNonNegativeInteger(pagination.page, "page", 1),
      page_size: requireNonNegativeInteger(pagination.page_size, "page_size", 1),
      total: requireNonNegativeInteger(pagination.total, "total", 0),
    },
  };
}

export function decodeReleaseDetail(input: unknown): ReleaseDetailDTO {
  const object = requireObject(input, "release detail");
  const summary = decodeReleaseSummary(object);
  const artworkObject = object.artwork === undefined ? undefined : requireObject(object.artwork, "artwork");
  return {
    ...summary,
    artwork: artworkObject ? { resource_id: requireNonEmptyString(artworkObject.resource_id, "artwork.resource_id"), mime: requireNonEmptyString(artworkObject.mime, "artwork.mime"), width: requireNonNegativeInteger(artworkObject.width, "artwork.width", 1), height: requireNonNegativeInteger(artworkObject.height, "artwork.height", 1) } : undefined,
    media: requireArray(object.media, "media").map((item) => {
      const medium = requireObject(item, "medium");
      return {
        id: requireNonEmptyString(medium.id, "medium.id"),
        position: requireNonNegativeInteger(medium.position, "medium.position", 1),
        title: requireNonEmptyString(medium.title, "medium.title"),
        tracks: requireArray(medium.tracks, "medium.tracks").map((trackItem) => {
          const track = requireObject(trackItem, "track");
          return {
            id: requireNonEmptyString(track.id, "track.id"),
            title: requireNonEmptyString(track.title, "track.title"),
            artist: requireNonEmptyString(track.artist, "track.artist"),
            position: requireNonNegativeInteger(track.position, "track.position", 1),
            source: requireNonEmptyString(track.source, "track.source"),
          };
        }),
      };
    }),
  };
}

export function decodeAcknowledgement(input: unknown): UnknownObject {
  return requireObject(input, "acknowledgement");
}

function decodeReleaseSummary(input: unknown): ReleaseSummaryDTO {
  const object = requireObject(input, "release summary");
  return {
    id: requireNonEmptyString(object.id, "release.id"),
    title: requireNonEmptyString(object.title, "release.title"),
    artist: requireNonEmptyString(object.artist, "release.artist"),
  };
}

export function decodeApiErrorResponse(input: unknown, fallbackRequestID: string | null): ApiRequestError {
  if (!isObject(input)) {
    return new ApiRequestError("request_failed", "请求失败", fallbackRequestID);
  }
  const errorDetail = isObject(input.error) ? input.error : null;
  const code = errorDetail && typeof errorDetail.code === "string" && errorDetail.code !== "" ? errorDetail.code : "request_failed";
  const message = errorDetail && typeof errorDetail.message === "string" && errorDetail.message !== "" ? errorDetail.message : "请求失败";
  const requestID = typeof input.request_id === "string" && input.request_id !== "" ? input.request_id : fallbackRequestID;
  return new ApiRequestError(code, message, requestID);
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

function requireNonEmptyString(input: unknown, fieldName: string): string {
  if (typeof input !== "string" || input.trim() === "") {
    throw new Error(`${fieldName} must be a non-empty string`);
  }
  return input;
}

function requireBoolean(input: unknown, fieldName: string): boolean {
  if (typeof input !== "boolean") {
    throw new Error(`${fieldName} must be a boolean`);
  }
  return input;
}

function requireTimestamp(input: unknown, fieldName: string): string {
  const timestamp = requireNonEmptyString(input, fieldName);
  if (Number.isNaN(Date.parse(timestamp))) {
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

const requireSafeInteger = requireNonNegativeInteger;
