import { isUuid } from "../../shared/lib/ids";
import { parsePageParam } from "../../shared/lib/pagination";

/**
 * “待认领”页的地址参数：page 只在 2 及以上时出现，release 是选中的旧版本（宽屏在右栏显示它的详情，窄屏只显示它），
 * target 是选中的新版本（认领要迁给它；专辑详情的“可能接替了……”提示点进来时预选当前版本）。选择写进地址，刷新与分享都保持。
 */
export type ClaimsSearch = {
  page?: number | undefined;
  release?: string | undefined;
  target?: string | undefined;
};

/**
 * 路由的 validateSearch：不合法的值显式写成 undefined（路由把校验结果合并到原始查询串上，只省略键的话原值会留在 search 里）。
 * release 与 target 只保留 UUID 形状的值并统一成小写；没有选中旧版本时 target 没有意义，一并丢弃；默认值（第 1 页）不写进地址。
 */
export function parseClaimsSearch(search: Record<string, unknown>): ClaimsSearch {
  const release = search["release"];
  const target = search["target"];
  const hasRelease = isUuid(release);
  return {
    page: parsePageParam(search["page"]),
    release: hasRelease ? release.toLowerCase() : undefined,
    target: hasRelease && isUuid(target) ? target.toLowerCase() : undefined,
  };
}
