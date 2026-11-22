import { isUuid } from "../../shared/lib/ids";
import { parsePageParam } from "../../shared/lib/pagination";

/** 操作记录页的地址参数：page 只在 2 及以上时出现；release 是只看这个版本的记录，group 是看这张专辑的记录，artist 是看这位艺人的记录（同时给出时取并集）。 */
export type OperationsSearch = {
  page?: number | undefined;
  release?: string | undefined;
  group?: string | undefined;
  artist?: string | undefined;
  /** 只看自动整理的记录（M7 E1）；后端只接受 curation。 */
  source?: "curation" | undefined;
  /** 只看来自这个外部源或模型的自动整理记录；隐含 source=curation。 */
  provider?: string | undefined;
  /** 只看这一批自动整理的记录。 */
  batch?: string | undefined;
};

const PROVIDER_PATTERN = /^[a-z0-9._:-]{1,64}$/;

/**
 * 路由的 validateSearch：不合法的值显式写成 undefined（路由把校验结果合并到原始查询串上，只省略键的话原值会留在 search 里）。
 * release 与 group 只保留 UUID 形状的值并统一成小写，与接口给的 ID 直接比较；不合法时不筛选，不向后端发无效的筛选。
 */
export function parseOperationsSearch(search: Record<string, unknown>): OperationsSearch {
  const release = search["release"];
  const group = search["group"];
  const artist = search["artist"];
  const provider = search["provider"];
  const batch = search["batch"];
  return {
    page: parsePageParam(search["page"]),
    release: isUuid(release) ? release.toLowerCase() : undefined,
    group: isUuid(group) ? group.toLowerCase() : undefined,
    artist: isUuid(artist) ? artist.toLowerCase() : undefined,
    source: search["source"] === "curation" ? "curation" : undefined,
    provider: typeof provider === "string" && PROVIDER_PATTERN.test(provider) ? provider : undefined,
    batch: isUuid(batch) ? batch.toLowerCase() : undefined,
  };
}
