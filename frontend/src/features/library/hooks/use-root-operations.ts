import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchRootOperations } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 目录操作记录：管理员可读最近 100 条，最新的在前。 */
export function useRootOperations() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.rootOperations(generation),
    queryFn: ({ signal }) => fetchRootOperations(signal),
    select: (response) => response.data.items,
  });
}
