import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchRelease } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/** 一个发行版本的详情（所有登录用户可读）：介质、曲目、署名与证据摘要。releaseId 来自组详情里的成员，是接口给的 UUID。 */
export function useRelease(releaseId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.release(generation, releaseId),
    queryFn: ({ signal }) => fetchRelease(releaseId, signal),
    select: (response) => response.data,
  });
}
