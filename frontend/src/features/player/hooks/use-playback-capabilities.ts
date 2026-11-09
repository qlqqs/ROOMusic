import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchPlaybackFormats } from "../api/endpoints";
import type { PlaybackFormatsDTO } from "../api/decoders";
import { buildCapabilityTable } from "../engine/capability";
import { playerKeys } from "../query-keys";
import type { ApiResponse } from "../../../shared/api";

/** 模块级的 select：引用不变，查询结果不变时判定表也不变，使用者的 memo 不会每次渲染都失效。 */
const tableOf = (response: ApiResponse<PlaybackFormatsDTO>) => buildCapabilityTable(response.data.items);

/** 服务端格式表在会话内缓存；请求失败仍是错误，不伪装成“没有可播格式”。 */
export function usePlaybackCapabilities() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: playerKeys.formats(generation),
    queryFn: ({ signal }) => fetchPlaybackFormats(signal),
    select: tableOf,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });
}
