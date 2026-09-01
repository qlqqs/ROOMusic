export type SetupStatusDTO = { setup_required: boolean };
export type SessionDTO = { username: string; role: "admin" | "user" };
export type UserDTO = { id: string; username: string; role: "admin" | "user"; disabled: boolean; created_at: string };
export type LibraryRootDTO = { id: string; path: string; created_at: string };
export type CreatedLibraryRootDTO = { id: string; name: string };

const scanStatuses = ["running", "succeeded", "failed", "canceled", "incomplete"] as const;
export type ScanStatusValue = (typeof scanStatuses)[number];
export type ScanStartDTO = { id: string; scan_run_id: string; status: "running" };
export type ScanStatusDTO = { id: string; status: ScanStatusValue; started_at: string; finished_at: string | null };

export type ReleaseSummaryDTO = { id: string; title: string; artist: string };
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
  const roleValue = object.role === undefined ? "admin" : requireNonEmptyString(object.role, "role"); const role = roleValue as "admin" | "user"; if (role !== "admin" && role !== "user") throw new Error("unknown role");
  return { username: requireNonEmptyString(object.username, "username"), role };
}
export function decodeUserList(input: unknown): {items: UserDTO[]} { const object=requireObject(input,"user list"); return {items: requireArray(object.items,"items").map(item=>{const u=requireObject(item,"user"); const role=requireNonEmptyString(u.role,"role"); if(role!=="admin"&&role!=="user") throw new Error("unknown role"); return {id:requireNonEmptyString(u.id,"id"),username:requireNonEmptyString(u.username,"username"),role,disabled:requireBoolean(u.disabled,"disabled"),created_at:requireTimestamp(u.created_at,"created_at")};})}; }

export function decodeLibraryRootList(input: unknown): { items: LibraryRootDTO[] } {
  const object = requireObject(input, "library root list");
  return {
    items: requireArray(object.items, "items").map((item) => {
      const root = requireObject(item, "library root");
      return {
        id: requireNonEmptyString(root.id, "id"),
        path: requireNonEmptyString(root.path, "path"),
        created_at: requireTimestamp(root.created_at, "created_at"),
      };
    }),
  };
}

export function decodeCreatedLibraryRoot(input: unknown): CreatedLibraryRootDTO {
  const object = requireObject(input, "created library root");
  return { id: requireNonEmptyString(object.id, "id"), name: requireNonEmptyString(object.name, "name") };
}

export function decodeScanStart(input: unknown): ScanStartDTO {
  const object = requireObject(input, "scan start");
  const status = requireNonEmptyString(object.status, "status");
  if (status !== "running") {
    throw new Error("scan start status must be running");
  }
  return {
    id: requireNonEmptyString(object.id, "id"),
    scan_run_id: requireNonEmptyString(object.scan_run_id, "scan_run_id"),
    status,
  };
}

export function decodeScanStatus(input: unknown): ScanStatusDTO {
  const object = requireObject(input, "scan status");
  const status = requireNonEmptyString(object.status, "status");
  if (!isScanStatus(status)) {
    throw new Error("unknown scan status");
  }
  return {
    id: requireNonEmptyString(object.id, "id"),
    status,
    started_at: requireTimestamp(object.started_at, "started_at"),
    finished_at: object.finished_at === null ? null : requireTimestamp(object.finished_at, "finished_at"),
  };
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
