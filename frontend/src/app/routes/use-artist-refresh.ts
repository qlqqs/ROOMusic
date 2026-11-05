import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useViewerScope } from "../../features/auth";
import { artistsKeys } from "../../features/artists";
import { useCatalogRefresh } from "../../features/catalog";
import { listeningKeys } from "../../features/listening";
import { personalKeys } from "../../features/personal";
import { searchKeys } from "../../features/search";
import { usePlayerMetadataRefresh } from "../player-metadata-refresh";

/**
 * 艺人治理（合并、拆开、改名）之后要重新读取的缓存（M5 ⑤）：艺人的全部查询、目录（专辑与曲目里的艺人引用）、全局搜索、
 * 本人的收藏（艺人一栏按合并后的艺人显示）与历史（曲目里的艺人引用）；刷新完之后再补全播放器队列里的艺人链接（I4）。
 * 由 app 组合：governance 不知道这些查询键。返回稳定的函数。
 */
export function useArtistDataRefresh(): () => Promise<unknown> {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const refreshCatalog = useCatalogRefresh();
  const refreshPlayer = usePlayerMetadataRefresh();
  return useCallback(
    () => {
      // 先移除没在显示的艺人详情：缓存里可能是合并后旧身份的重定向结果（拆回之后已经不对了），打开这个艺人页时不能先拿它跳走；
      // 正在显示的详情随下面的失效重新读取。
      queryClient.removeQueries({ queryKey: artistsKeys.details(generation), type: "inactive" });
      return Promise.all([
        refreshCatalog(),
        queryClient.invalidateQueries({ queryKey: artistsKeys.all(generation) }),
        queryClient.invalidateQueries({ queryKey: searchKeys.all(generation) }),
        queryClient.invalidateQueries({ queryKey: personalKeys.favorites(generation) }),
        queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
      ]).then(refreshPlayer);
    },
    [refreshCatalog, queryClient, generation, refreshPlayer],
  );
}
