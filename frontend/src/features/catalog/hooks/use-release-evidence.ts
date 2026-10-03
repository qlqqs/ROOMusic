import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseEvidence } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/**
 * 一个发行版本的完整证据（仅管理员）。enabled 为 false 时不创建请求：普通用户不会调用这个接口，
 * 管理员也只在点了“查看完整证据”之后才请求。
 */
export function useReleaseEvidence(releaseId: string, enabled: boolean) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.evidence(generation, releaseId),
    queryFn: ({ signal }) => fetchReleaseEvidence(releaseId, signal),
    select: (response) => response.data,
    enabled,
  });
}
