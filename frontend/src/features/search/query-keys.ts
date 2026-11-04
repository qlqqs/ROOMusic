import { viewerQueryKey } from "../auth";

// g 是会话代次，e 是目录状态的 epoch（搜索数据的代次），两者不能混为一谈：epoch 变大后旧代次的结果留在旧键下，不会被当成新结果显示。
// 键里的 q 是去掉首尾空白后的用户查询，不在浏览器里做规范化。
export const searchKeys = {
  /** 搜索的全部查询：修改专辑数据、epoch 变化后整体失效或取消。 */
  all: (generation: number) => viewerQueryKey(generation, "search"),
  suggestions: (generation: number, epoch: number, q: string, limit: number) => viewerQueryKey(generation, "search", "suggestions", { e: epoch, q, limit }),
  page: (generation: number, epoch: number, type: "albums" | "tracks", q: string, page: number, pageSize: number) =>
    viewerQueryKey(generation, "search", "results", { e: epoch, type, q, page, pageSize }),
};

/** 查询键里记录的 epoch；不是搜索的键时返回 null。用于取消属于旧代次的请求。 */
export function epochOfSearchKey(key: readonly unknown[]): number | null {
  const last = key.at(-1);
  if (typeof last !== "object" || last === null || !("e" in last)) return null;
  return typeof last.e === "number" ? last.e : null;
}
