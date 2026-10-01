import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { decodeMemberList, type MemberListDTO } from "./decoders";

export function fetchMembers(signal?: AbortSignal): Promise<ApiResponse<MemberListDTO>> {
  return apiRequest({ method: "GET", path: "/api/v1/users", signal }, decodeMemberList);
}
