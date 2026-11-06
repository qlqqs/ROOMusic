import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchBuildInfo } from "../api/endpoints";
import { systemKeys } from "../query-keys";

/** 当前运行的版本：进程不重启就不会变，换版本后整页刷新才会重新读取，所以永不过期。 */
export function useBuildInfo() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: systemKeys.version(generation),
    queryFn: ({ signal }) => fetchBuildInfo(signal),
    select: (response) => response.data,
    staleTime: Number.POSITIVE_INFINITY,
  });
}
