import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchTrackLyrics } from "../api/endpoints";
import { playerKeys } from "../query-keys";

/**
 * 一首曲目的内嵌歌词（所有登录成员可读）：data 是 null 表示曲目没有可显示的歌词。歌词只在重新扫描时变化，
 * 所以会话内不过期；会话结束时随查看者代次清空。trackId 是队列里的曲目标识（后端给的 UUID）。
 */
export function useTrackLyrics(trackId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: playerKeys.lyrics(generation, trackId),
    queryFn: ({ signal }) => fetchTrackLyrics(trackId, signal),
    select: (response) => response.data.lyrics,
    staleTime: Infinity,
  });
}
