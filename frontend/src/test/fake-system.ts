import type { FakeApi, FakeSession } from "./fake-api";
import { jsonResponse, sampleResponse } from "./http";
import { backupFileAt, backupNotFound, respondToSystem, systemDataOf, type SystemData } from "./system-data";

/**
 * “系统 › 备份”接口的替身：版本、备份状态与列表、立即备份（行为见 system-data.ts）。未登录 401、非管理员 403。
 * 时钟从 2026-11-06 12:00:00Z 起每次取值前进一秒。需要特殊响应（网络失败、503、特定错误码）的测试用 api.on 覆盖对应路由。
 */
export function installSystemApi(api: FakeApi, session: FakeSession, overrides: Partial<SystemData> = {}): SystemData {
  const data = systemDataOf(overrides);
  let tick = 0;
  const now = () => new Date(Date.UTC(2026, 10, 6, 12, 0, tick++)).toISOString().replace(".000Z", "Z");
  const admin = (): Response | null => {
    if (session.current === null) return sampleResponse("errors/unauthorized");
    return session.accounts[session.current]?.role === "admin" ? null : sampleResponse("errors/forbidden");
  };
  const answer = (method: string) => (request: { path: string }) => {
    const denied = admin();
    if (denied !== null) return denied;
    const result = respondToSystem(data, method, request.path.split("?")[0] ?? request.path, now);
    return result === null ? sampleResponse("errors/not-found-api") : jsonResponse(result.status, result.body);
  };
  api.on("GET", "/api/v1/system/version", answer("GET"));
  api.on("GET", "/api/v1/system/backups", answer("GET"));
  api.on("POST", "/api/v1/system/backups", answer("POST"));
  // 下载不是 JSON：页面用直链交给浏览器，这里只为直接请求的测试给出附件响应或 404。
  api.on("GET", /^\/api\/v1\/system\/backups\/[^/]+$/, (request) => {
    const denied = admin();
    if (denied !== null) return denied;
    const file = backupFileAt(data, request.path.split("?")[0] ?? request.path);
    if (file === null) {
      const missing = backupNotFound();
      return jsonResponse(missing.status, missing.body);
    }
    return new Response("PGDMP", {
      status: 200,
      headers: { "Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="${file.name}"` },
    });
  });
  return data;
}
