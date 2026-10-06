import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchPlaybackFormats } from "../api/endpoints";
import { browserProbe, buildCapabilityTable } from "../engine/capability";
import { playerKeys } from "../query-keys";

/** 后端批准表与浏览器类型探测都在会话内缓存；请求失败仍是错误，不伪装成“没有可播格式”。 */
export function usePlaybackCapabilities() {
  const { generation } = useViewerScope();
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: playerKeys.capabilities(generation),
    staleTime: Infinity,
    gcTime: Infinity,
    queryFn: async () => {
      const response = await queryClient.fetchQuery({
        queryKey: playerKeys.formats(generation),
        queryFn: ({ signal }) => fetchPlaybackFormats(signal),
        staleTime: Infinity,
        gcTime: Infinity,
        retry: false,
      });
      return buildCapabilityTable(response.data.items, browserProbe());
    },
  });
}
