import { curationDataOf, respondToAcceptException, respondToClearSuppression, respondToCurationConversations, respondToCurationExceptions, respondToDeclineException, respondToCurationBatch, respondToCurationBatches, respondToCurationProgress, respondToCurationSettings, respondToCurationSources, respondToScopeRevert, respondToSetCurationSource, respondToSetCurationSourceKey, respondToSuppressions, respondToUpdateCurationSettings, type CurationData } from "./curation-data";
import { failure } from "./catalog-data";
import type { FakeApi } from "./fake-api";
import type { GovernanceData } from "./governance-data";
import { jsonResponse } from "./http";

const idFrom = (path: string, pattern: RegExp): string => decodeURIComponent(pattern.exec(path.split("?")[0] ?? path)?.[1] ?? "");
const searchOf = (path: string) => new URL(path, "http://localhost").searchParams;

/**
 * 自动整理管理员接口的替身（行为见 curation-data.ts）。登录外壳默认装一份（没有操作记录、曲库里没有发行，概览不出现一次性提示）；
 * 需要批次与范围撤销的测试传入 governance 替身再装一次，后装的覆盖先装的。需要特殊响应（网络失败、特定错误码）的测试用 api.on 覆盖对应路由。
 */
export function installCurationApi(api: FakeApi, governance: GovernanceData | null = null): CurationData {
  const data = curationDataOf(governance);
  const answer = (result: { status: number; body: unknown }) => (result.status === 204 ? new Response(null, { status: 204 }) : jsonResponse(result.status, result.body));
  const base = "/api/v1/admin/curation";
  api.on("GET", `${base}/settings`, () => answer(respondToCurationSettings(data)));
  api.on("PUT", `${base}/settings`, (request) => answer(respondToUpdateCurationSettings(data, request.body)));
  api.on("GET", `${base}/sources`, () => answer(respondToCurationSources(data)));
  api.on("PUT", /^\/api\/v1\/admin\/curation\/sources\/[^/]+$/, (request) => answer(respondToSetCurationSource(data, idFrom(request.path, /\/sources\/([^/]+)$/), request.body)));
  const keyPath = /^\/api\/v1\/admin\/curation\/sources\/([^/]+)\/keys\/([^/]+)$/;
  const keyName = (path: string) => decodeURIComponent(keyPath.exec(path.split("?")[0] ?? path)?.[2] ?? "");
  api.on("PUT", keyPath, (request) => {
    const body = request.body as { key?: unknown } | null;
    if (typeof body?.key !== "string") return answer(failure(400, "invalid_input", "请求体无效"));
    return answer(respondToSetCurationSourceKey(data, idFrom(request.path, keyPath), keyName(request.path), body.key));
  });
  api.on("DELETE", keyPath, (request) => answer(respondToSetCurationSourceKey(data, idFrom(request.path, keyPath), keyName(request.path), null)));
  api.on("GET", `${base}/progress`, () => answer(respondToCurationProgress(data)));
  api.on("GET", `${base}/batches`, (request) => answer(respondToCurationBatches(data, searchOf(request.path))));
  api.on("GET", /^\/api\/v1\/admin\/curation\/batches\/[^/]+$/, (request) => answer(respondToCurationBatch(data, idFrom(request.path, /\/batches\/([^/]+)$/))));
  api.on("POST", `${base}/reverts`, (request) => answer(respondToScopeRevert(data, request.headers.get("Idempotency-Key"), request.body)));
  api.on("GET", `${base}/suppressions`, (request) => answer(respondToSuppressions(data, searchOf(request.path))));
  api.on("DELETE", /^\/api\/v1\/admin\/curation\/suppressions\/[^/]+$/, (request) => answer(respondToClearSuppression(data, idFrom(request.path, /\/suppressions\/([^/]+)$/))));
  api.on("GET", `${base}/exceptions`, (request) => answer(respondToCurationExceptions(data, searchOf(request.path))));
  api.on("POST", /^\/api\/v1\/admin\/curation\/exceptions\/[^/]+\/accept$/, (request) => answer(respondToAcceptException(data, idFrom(request.path, /\/exceptions\/([^/]+)\/accept$/))));
  api.on("POST", /^\/api\/v1\/admin\/curation\/exceptions\/[^/]+\/decline$/, (request) => answer(respondToDeclineException(data, idFrom(request.path, /\/exceptions\/([^/]+)\/decline$/))));
  api.on("GET", `${base}/conversations`, (request) => answer(respondToCurationConversations(data, searchOf(request.path))));
  return data;
}
