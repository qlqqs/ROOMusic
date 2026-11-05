import { artistDataOf, artistSummaryOriginError, respondToArtistFavorites, respondToArtists, respondToArtistSummaries, type ArtistData } from "./artist-data";
import { respondToPersonal } from "./personal-data";
import type { FakeApi, FakeSession } from "./fake-api";
import { jsonResponse, sampleResponse } from "./http";

/** 艺人域的 HTTP 适配器；通用响应函数不依赖 Vitest，浏览器层可以直接复用。 */
export function installArtistsApi(api: FakeApi, data: ArtistData = artistDataOf(), session?: FakeSession, options: { browserOrigin?: string | null } = {}): ArtistData {
  // fetch 在真实浏览器自动补 Origin；边界测试传 null，明确模拟没有来源的 HTTP 请求。
  const browserOrigin = options.browserOrigin === undefined ? "http://localhost" : options.browserOrigin;
  api.on("POST", "/api/v1/tracks/summaries", (request) => {
    const error = artistSummaryOriginError(request.headers.get("Origin") ?? browserOrigin, new URL(request.path, "http://localhost").origin);
    if (error !== null) return jsonResponse(error.status, error.body);
    if (session !== undefined && (session.current === null || session.accounts[session.current] === undefined)) return sampleResponse("errors/unauthorized");
    const response = respondToArtistSummaries(data, request.body);
    return jsonResponse(response.status, response.body);
  });
  for (const method of ["GET", "POST", "PATCH"]) {
    api.on(method, /^\/api\/v1\/(?:artists(?:\/.*)?|artist-candidates)$/, (request) => {
      const url = new URL(request.path, "http://localhost");
      if (session !== undefined) {
        const account = session.current === null ? undefined : session.accounts[session.current];
        if (account === undefined) return sampleResponse("errors/unauthorized");
        const admin = method !== "GET" || url.pathname === "/api/v1/artist-candidates" || url.pathname.endsWith("/merged-sources");
        if (admin && account.role !== "admin") return sampleResponse("errors/forbidden");
      }
      const response = respondToArtists(data, method, url, request.body, request.headers.get("Idempotency-Key"));
      return jsonResponse(response.status, response.body);
    });
  }
  return data;
}

/** 艺人收藏只接管 artist_ids lookup，其余沿用 personal 替身，避免把曲目心形变成未知状态。 */
export function installArtistFavoritesApi(api: FakeApi, data: ArtistData, session: FakeSession, personal: import("./personal-data").PersonalData): void {
  const userId = () => session.current === null ? null : session.accounts[session.current]?.id ?? null;
  api.on("POST", "/api/v1/me/favorites/lookup", (request) => {
    const id = userId();
    if (id === null) return sampleResponse("errors/unauthorized");
    const hasArtists = typeof request.body === "object" && request.body !== null && "artist_ids" in request.body;
    const result = hasArtists ? respondToArtistFavorites(data, request.method, new URL(request.path, "http://localhost"), request.body, id) : respondToPersonal(personal, id, { method: request.method, path: request.path, body: request.body, idempotencyKey: null });
    return jsonResponse(result.status, result.body);
  });
  for (const method of ["GET", "PUT", "DELETE"]) api.on(method, /^\/api\/v1\/me\/favorites\/artists(?:\/[^/]+)?$/, (request) => {
    const id = userId();
    if (id === null) return sampleResponse("errors/unauthorized");
    const result = respondToArtistFavorites(data, method, new URL(request.path, "http://localhost"), request.body, id);
    return jsonResponse(result.status, result.body);
  });
}
