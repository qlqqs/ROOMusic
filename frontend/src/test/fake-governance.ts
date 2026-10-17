import type { FakeApi } from "./fake-api";
import type { FakeCatalog } from "./fake-catalog";
import { jsonResponse } from "./http";
import {
  governanceDataOf,
  respondToClaim,
  respondToClaimHints,
  respondToClaimPreview,
  respondToClaimRelease,
  respondToClaims,
  respondToCorrect,
  respondToCorrections,
  respondToGroupChange,
  respondToGroupDefault,
  respondToIssueReview,
  respondToIssues,
  respondToOperations,
  respondToReleaseIssues,
  respondToRevert,
  type GovernanceData,
} from "./governance-data";

const idFrom = (path: string, pattern: RegExp): string => pattern.exec(path.split("?")[0] ?? path)?.[1] ?? "";

/**
 * 受治理操作接口的替身：修正状态、字段修正、归组、默认版本、元数据问题与核对、操作记录与撤销。行为见 governance-data.ts（预期版本、幂等重放、各种 409），
 * 修改会反映到共用的专辑替身（列表、组详情、版本详情）。需要特殊响应（网络失败、503、特定错误码）的测试用 api.on 覆盖对应路由。
 */
export function installGovernanceApi(api: FakeApi, catalog: FakeCatalog): GovernanceData {
  const data = governanceDataOf(catalog);
  const answer = (result: { status: number; body: unknown }) => jsonResponse(result.status, result.body);
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/corrections$/, (request) => answer(respondToCorrections(data, idFrom(request.path, /\/releases\/([^/]+)\/corrections$/))));
  api.on("PATCH", /^\/api\/v1\/releases\/[^/]+\/corrections$/, (request) =>
    answer(respondToCorrect(data, idFrom(request.path, /\/releases\/([^/]+)\/corrections$/), request.headers.get("Idempotency-Key"), request.body)),
  );
  api.on("POST", /^\/api\/v1\/releases\/[^/]+\/group$/, (request) =>
    answer(respondToGroupChange(data, idFrom(request.path, /\/releases\/([^/]+)\/group$/), request.headers.get("Idempotency-Key"), request.body)),
  );
  api.on("POST", /^\/api\/v1\/release-groups\/[^/]+\/default$/, (request) =>
    answer(respondToGroupDefault(data, idFrom(request.path, /\/release-groups\/([^/]+)\/default$/), request.headers.get("Idempotency-Key"), request.body)),
  );
  api.on("GET", "/api/v1/release-issues", (request) => answer(respondToIssues(data, new URL(request.path, "http://localhost").searchParams)));
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/issues$/, (request) => answer(respondToReleaseIssues(data, idFrom(request.path, /\/releases\/([^/]+)\/issues$/))));
  api.on("POST", /^\/api\/v1\/releases\/[^/]+\/issue-review$/, (request) =>
    answer(respondToIssueReview(data, idFrom(request.path, /\/releases\/([^/]+)\/issue-review$/), request.headers.get("Idempotency-Key"), request.body)),
  );
  api.on("GET", "/api/v1/release-claims", (request) => answer(respondToClaims(data, new URL(request.path, "http://localhost").searchParams)));
  api.on("GET", /^\/api\/v1\/release-claims\/[^/]+$/, (request) => answer(respondToClaim(data, idFrom(request.path, /\/release-claims\/([^/]+)$/))));
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/claim-hints$/, (request) => answer(respondToClaimHints(data, idFrom(request.path, /\/releases\/([^/]+)\/claim-hints$/))));
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/claim-preview$/, (request) =>
    answer(respondToClaimPreview(data, idFrom(request.path, /\/releases\/([^/]+)\/claim-preview/), new URL(request.path, "http://localhost").searchParams)),
  );
  api.on("POST", /^\/api\/v1\/releases\/[^/]+\/claim$/, (request) =>
    answer(respondToClaimRelease(data, idFrom(request.path, /\/releases\/([^/]+)\/claim$/), request.headers.get("Idempotency-Key"), request.body)),
  );
  api.on("GET", "/api/v1/operations", (request) => answer(respondToOperations(data, new URL(request.path, "http://localhost").searchParams)));
  api.on("POST", /^\/api\/v1\/operations\/[^/]+\/revert$/, (request) =>
    answer(respondToRevert(data, idFrom(request.path, /\/operations\/([^/]+)\/revert$/), request.headers.get("Idempotency-Key"))),
  );
  return data;
}
