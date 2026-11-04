import type { CatalogData } from "./catalog-data";
import type { FakeApi, FakeSession } from "./fake-api";
import { jsonResponse } from "./http";
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
  return data;
}
