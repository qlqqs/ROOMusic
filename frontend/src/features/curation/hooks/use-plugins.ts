import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { deletePlugin, fetchPlugins, installPlugin, previewPlugin } from "../api/plugins";
import { curationKeys } from "../query-keys";

/** 已安装的插件（管理员）。 */
export function usePlugins() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: curationKeys.plugins(generation), queryFn: ({ signal }) => fetchPlugins(signal), select: (response) => response.data.items });
}

/** 上传做预检：只解析与检查，不安装。 */
export function usePreviewPlugin() {
  return useMutation({ mutationFn: (file: File) => previewPlugin(file) });
}

/** 安装与删除之后，插件列表与外部源列表（插件就是一个外部源）都要重读。 */
function useRefreshPlugins() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return () => {
    if (!scope.isCurrent()) return;
    void queryClient.invalidateQueries({ queryKey: curationKeys.plugins(scope.generation) });
    void queryClient.invalidateQueries({ queryKey: curationKeys.sources(scope.generation) });
  };
}

export function useInstallPlugin() {
  const refresh = useRefreshPlugins();
  return useMutation({ mutationFn: (previewToken: string) => installPlugin(previewToken), onSuccess: refresh });
}

export function useDeletePlugin() {
  const refresh = useRefreshPlugins();
  return useMutation({ mutationFn: (id: string) => deletePlugin(id), onSuccess: refresh });
}
