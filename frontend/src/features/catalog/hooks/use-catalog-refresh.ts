import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useViewerScope } from "../../auth";
import { catalogKeys } from "../query-keys";

/**
 * 让当前会话里缓存的全部专辑数据（列表、组、版本详情、完整证据）失效并重新读取正在显示的部分。
 * 别的 feature 的写操作改变了专辑数据（修正、归组、撤销）之后用它刷新；返回的函数在同一个查看者范围内保持稳定。
 */
export function useCatalogRefresh(): () => Promise<void> {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useCallback(() => queryClient.invalidateQueries({ queryKey: catalogKeys.all(generation) }), [queryClient, generation]);
}
