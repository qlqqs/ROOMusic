import { useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchCorrections } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/**
 * 编辑发行信息的基线（仅管理员）：打开对话框时才读取，每次打开都重新读，关闭后不保留缓存（gcTime 0）——
 * 基线里的人工修改版本号决定能不能提交，不能拿旧缓存冒充。编辑过程中不因窗口重新聚焦而刷新；
 * 修改后的失效刷新由命令负责，基线变化时草稿的处理见 field-draft.ts。
 */
export function useReleaseCorrections(releaseId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.corrections(generation, releaseId),
    queryFn: ({ signal }) => fetchCorrections(releaseId, signal),
    select: (response) => response.data,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
}
