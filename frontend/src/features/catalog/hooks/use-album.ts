import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchReleaseGroup } from "../api/endpoints";
import { catalogKeys } from "../query-keys";

/** 一个专辑组的详情（所有登录用户可读）。groupId 由调用方先校验为 UUID。 */
export function useAlbum(groupId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: catalogKeys.group(generation, groupId),
    queryFn: ({ signal }) => fetchReleaseGroup(groupId, signal),
    select: (response) => response.data,
  });
}
