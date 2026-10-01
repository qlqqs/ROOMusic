import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchActiveScan } from "../api/endpoints";
import { libraryKeys } from "../query-keys";

/** 最早开始的运行中扫描；没有时为 null。任何已登录用户都可以读取。 */
export function useActiveScan() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: libraryKeys.activeScan(generation),
    queryFn: ({ signal }) => fetchActiveScan(signal),
    select: (response) => response.data.scan,
  });
}
