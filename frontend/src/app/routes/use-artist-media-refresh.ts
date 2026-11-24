import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useViewerScope } from "../../features/auth";
import { artistsKeys } from "../../features/artists";
import { searchKeys } from "../../features/search";

/**
 * 管理员改了艺人的图片、简介或对应之后要重新读取的缓存：艺人的全部查询（详情、列表里的小圆章）与全局搜索（艺人结果的圆章）。
 * 由 app 组合：artists 不知道搜索的查询键。返回稳定的函数。
 */
export function useArtistMediaRefresh(): () => Promise<unknown> {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useCallback(
    () => Promise.all([queryClient.invalidateQueries({ queryKey: artistsKeys.all(generation) }), queryClient.invalidateQueries({ queryKey: searchKeys.all(generation) })]),
    [queryClient, generation],
  );
}
