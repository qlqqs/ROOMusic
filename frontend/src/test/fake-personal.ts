import type { CatalogData } from "./catalog-data";
import type { FakeApi, FakeSession } from "./fake-api";
import { jsonResponse, sampleResponse } from "./http";
import { personalDataOf, respondToPersonal, type PersonalData } from "./personal-data";

export type PersonalApiOptions = {
  session: FakeSession;
  catalog?: CatalogData;
  data?: PersonalData;
};

/** 默认空集合；需要内容时用同一个 data 的 seed helper，特殊错误仍由 api.on 精确覆盖。 */
export function installPersonalApi(api: FakeApi, options: PersonalApiOptions): PersonalData {
  const data = options.data ?? personalDataOf(options.catalog);
  for (const method of ["GET", "POST", "PUT", "PATCH", "DELETE"]) {
    api.on(method, /^\/api\/v1\/me\/(favorites|playlists)(\/.*)?$/, (request) => {
      const session = options.session;
      const userId = session.current === null ? null : session.accounts[session.current]?.id ?? null;
      const result = respondToPersonal(data, userId, { method: request.method, path: request.path, body: request.body, idempotencyKey: request.headers.get("Idempotency-Key") });
      return jsonResponse(result.status, result.body, { "Cache-Control": "no-store" });
    });
  }
  installRatingApi(api, options.session);
  return data;
}

const ratingKinds = { tracks: "track", albums: "album", artists: "artist" } as const;

/**
 * 评分的替身：每个用户一份内存里的评分，按“类型:ID”存。设置、清除与批量读取的形状同后端；
 * 艺人按输入的 ID 回答，resolved_artist_id 就是它自己（替身里没有合并）。
 */
function installRatingApi(api: FakeApi, session: FakeSession) {
  const ratings = new Map<string, { rating: number; ratedAt: string }>();
  const userOf = () => (session.current === null ? null : (session.accounts[session.current]?.id ?? null));
  const unauthorized = () => sampleResponse("errors/personal-unauthorized");
  const key = (user: string, kind: string, id: string) => `${user}:${kind}:${id.toLowerCase()}`;
  for (const method of ["PUT", "DELETE"]) {
    api.on(method, /^\/api\/v1\/me\/ratings\/(tracks|albums|artists)\/[^/]+$/, (request) => {
      const user = userOf();
      if (user === null) return unauthorized();
      const [, , , , , segment = "", id = ""] = (request.path.split("?")[0] ?? "").split("/");
      const kind = ratingKinds[segment as keyof typeof ratingKinds];
      const body = request.body as { rating?: unknown } | null;
      if (request.method === "PUT") {
        const rating = body?.rating;
        if (typeof rating !== "number" || !Number.isInteger(rating) || rating < 1 || rating > 5) return sampleResponse("errors/personal-invalid-input");
        const ratedAt = new Date().toISOString();
        ratings.set(key(user, kind, id), { rating, ratedAt });
        return jsonResponse(200, { target_kind: kind, target_id: id, ...(kind === "artist" ? { resolved_artist_id: id } : {}), rating, rated_at: ratedAt });
      }
      ratings.delete(key(user, kind, id));
      return jsonResponse(200, { target_kind: kind, target_id: id, ...(kind === "artist" ? { resolved_artist_id: id } : {}), rating: null, rated_at: null });
    });
  }
  api.on("POST", "/api/v1/me/ratings/lookup", (request) => {
    const user = userOf();
    if (user === null) return unauthorized();
    const body = (request.body ?? {}) as { track_ids?: string[]; group_ids?: string[]; artist_ids?: string[] };
    const rated = (kind: string, id: string) => ratings.get(key(user, kind, id))?.rating ?? null;
    const unique = (ids: readonly string[] = []) => [...new Map(ids.map((id) => [id.toLowerCase(), id])).values()];
    return jsonResponse(200, {
      tracks: unique(body.track_ids).map((id) => ({ track_id: id, rating: rated("track", id) })),
      albums: unique(body.group_ids).map((id) => ({ group_id: id, rating: rated("album", id) })),
      artists: unique(body.artist_ids).map((id) => ({ artist_id: id, resolved_artist_id: id, rating: rated("artist", id) })),
    });
  });
}
