import { catalogDataOf, respondToGroupComparison, respondToGroupDetail, respondToGroupList, respondToReleaseDetail, respondToReleaseEvidence, type ApiResult, type CatalogData } from "./catalog-data";
import type { FakeApi } from "./fake-api";
import { jsonResponse } from "./http";

/**
 * 专辑接口替身的状态：测试可以直接读写，用来预置专辑或模拟“别处改了数据”。
 * 数据与各接口的行为见 catalog-data.ts：没有专门登记的组、版本与证据按列表里的摘要合成。
 */
export type FakeCatalog = CatalogData & {
  /** 设置后，对应接口的请求都返回它（例如 503 的 database_unavailable）；改回 null 恢复正常。 */
  listFailure: Response | null;
  groupFailure: Response | null;
  comparisonFailure: Response | null;
  releaseFailure: Response | null;
  evidenceFailure: Response | null;
};

const idFrom = (path: string, pattern: RegExp): string => pattern.exec(path.split("?")[0] ?? path)?.[1] ?? "";

/**
 * 专辑接口的替身：列表（也回答概览的“专辑数”请求：page_size=1 加 attention=required 时给出需要关注的数量）、
 * 组详情、版本对比、版本详情与完整证据。完整证据的管理员鉴权不在这里模拟：测试用 evidenceFailure 返回 403，或断言普通用户没有请求过它。
 */
export function installCatalogApi(api: FakeApi, overrides: Partial<FakeCatalog> = {}): FakeCatalog {
  const catalog: FakeCatalog = {
    ...catalogDataOf(),
    listFailure: null,
    groupFailure: null,
    comparisonFailure: null,
    releaseFailure: null,
    evidenceFailure: null,
    ...overrides,
  };
  const answer = (failure: Response | null, result: () => ApiResult): Response => {
    if (failure !== null) return failure.clone();
    const { status, body } = result();
    return jsonResponse(status, body);
  };
  api.on("GET", "/api/v1/release-groups", (request) =>
    answer(catalog.listFailure, () => respondToGroupList(catalog.groups, new URL(request.path, "http://localhost").searchParams)),
  );
  api.on("GET", /^\/api\/v1\/release-groups\/[^/]+$/, (request) =>
    answer(catalog.groupFailure, () => respondToGroupDetail(catalog, idFrom(request.path, /\/release-groups\/([^/]+)$/))),
  );
  api.on("GET", /^\/api\/v1\/release-groups\/[^/]+\/comparison$/, (request) =>
    answer(catalog.comparisonFailure, () => respondToGroupComparison(catalog, idFrom(request.path, /\/release-groups\/([^/]+)\/comparison$/))),
  );
  api.on("GET", /^\/api\/v1\/releases\/[^/]+$/, (request) =>
    answer(catalog.releaseFailure, () => respondToReleaseDetail(catalog, idFrom(request.path, /\/releases\/([^/]+)$/))),
  );
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/evidence$/, (request) =>
    answer(catalog.evidenceFailure, () => respondToReleaseEvidence(catalog, idFrom(request.path, /\/releases\/([^/]+)\/evidence$/))),
  );
  return catalog;
}
