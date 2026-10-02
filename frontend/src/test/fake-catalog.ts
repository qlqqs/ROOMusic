import type { ReleaseGroupSummaryDTO } from "../features/catalog/api/decoders";
import { respondToGroupList } from "./catalog-data";
import type { FakeApi } from "./fake-api";
import { jsonResponse } from "./http";

/** 专辑接口替身的状态：测试可以直接读写，用来预置专辑或模拟“别处改了数据”。 */
export type FakeCatalog = {
  /** 列表里的全部组，已按接口应有的顺序排好：替身只做筛选与分页，不重新排序，用来证明页面按接口的顺序显示。 */
  groups: ReleaseGroupSummaryDTO[];
  /** 设置后，列表请求都返回它（例如 503 的 database_unavailable）；改回 null 恢复正常。 */
  listFailure: Response | null;
};

/**
 * 专辑列表接口的替身，行为见 catalog-data.ts 的 respondToGroupList。
 * 同一个替身也回答概览的“专辑数”请求（page_size=1 加 attention=required 时给出需要关注的数量）。
 */
export function installCatalogApi(api: FakeApi, overrides: Partial<FakeCatalog> = {}): FakeCatalog {
  const catalog: FakeCatalog = { groups: [], listFailure: null, ...overrides };
  api.on("GET", "/api/v1/release-groups", (request) => {
    if (catalog.listFailure !== null) return catalog.listFailure.clone();
    const { status, body } = respondToGroupList(catalog.groups, new URL(request.path, "http://localhost").searchParams);
    return jsonResponse(status, body);
  });
  return catalog;
}
