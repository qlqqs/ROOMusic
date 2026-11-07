import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useViewerScope } from "../../features/auth";
import { useCatalogRefresh, useCatalogState } from "../../features/catalog";
import { epochOfSearchKey, searchKeys } from "../../features/search";
import { artistsKeys } from "../../features/artists";
import { listeningKeys } from "../../features/listening";
import { governanceKeys } from "../../features/governance";
import { personalKeys } from "../../features/personal";

/**
 * 已登录外壳读取目录状态（挂载、回到前台、准备期间每 3 秒），epoch 变大时：取消还在路上的旧代次搜索请求，
 * 并让目录缓存失效（专辑页带搜索词的结果换成新的匹配方式，专辑与曲目里的艺人引用随艺人数据发布出现）；
 * M5 ⑤ 起同时让艺人、“重复艺人”的候选、本人的收藏（艺人一栏与艺人的收藏状态）与历史（曲目里的艺人引用）失效；
 * 艺人数据从整理中变为就绪时（即使 epoch 没变）也这样做。搜索与艺人自己的查询键带 epoch，新代次自然重新请求；
 * 不动地址里的查询词与页码，不碰播放器。由 app 协调：catalog 与 search 互不知道对方的查询键。
 */
export function useCatalogEpochSync(): void {
  const state = useCatalogState();
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const refreshCatalog = useCatalogRefresh();
  const epoch = state.data?.epoch;
  const artistsState = state.data?.artists_state;
  const seen = useRef<{ epoch: number; artists: string } | undefined>(undefined);
  useEffect(() => {
    if (epoch === undefined || artistsState === undefined) return;
    const previous = seen.current;
    seen.current = { epoch, artists: artistsState };
    if (previous === undefined) return;
    const epochRaised = epoch > previous.epoch;
    // 艺人数据变为就绪（通常 epoch 同时变大；即使没变也要重读）：整理中读到 503 的艺人列表、艺人页、收藏的艺人与候选都要重新读取。
    const artistsPublished = artistsState === "ready" && previous.artists !== "ready";
    if (!epochRaised && !artistsPublished) return;
    if (epochRaised) {
      void queryClient.cancelQueries({
        queryKey: searchKeys.all(generation),
        predicate: (query) => {
          const keyEpoch = epochOfSearchKey(query.queryKey);
          return keyEpoch !== null && keyEpoch < epoch;
        },
      });
    }
    void refreshCatalog();
    void queryClient.invalidateQueries({ queryKey: artistsKeys.all(generation) });
    void queryClient.invalidateQueries({ queryKey: governanceKeys.artistCandidates(generation) });
    void queryClient.invalidateQueries({ queryKey: personalKeys.favorites(generation) });
    void queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) });
  }, [epoch, artistsState, queryClient, generation, refreshCatalog]);
}
