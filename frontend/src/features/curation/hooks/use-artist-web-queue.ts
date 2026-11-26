import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchArtistWebQueue, startArtistWebQueue, stopArtistWebQueue } from "../api/endpoints";
import { curationKeys } from "../query-keys";

/** “整理艺人”全量队列的状态（管理员）：在查时每 10 秒看一次进展，其余时候不轮询。 */
export function useArtistWebQueue() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.artistWebQueue(generation),
    queryFn: ({ signal }) => fetchArtistWebQueue(signal),
    select: (response) => response.data,
    refetchInterval: (query) => (query.state.data?.data.state === "running" ? 10_000 : false),
  });
}

/** 开始（或接续）与停止“整理艺人”：成功后把返回的队列状态写进缓存；失败时缓存不动，由调用方就近提示。 */
export function useArtistWebQueueCommand() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "artist-web-queue" },
    mutationFn: (command: "start" | "stop") => (command === "start" ? startArtistWebQueue() : stopArtistWebQueue()),
    onSuccess: (response) => {
      if (!scope.isCurrent()) return;
      queryClient.setQueryData(curationKeys.artistWebQueue(scope.generation), response);
    },
  });
}
