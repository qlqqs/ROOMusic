import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { confirmSubsonicPassword, fetchSubsonicConnection, fetchSubsonicSettings, setJukeboxZone, setSubsonicEnabled } from "../api/endpoints";
import { subsonicKeys } from "../query-keys";

/** 本人的连接状态；账号页据此决定显不显示“第三方客户端”一组。 */
export function useSubsonicConnection() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: subsonicKeys.connection(generation), queryFn: ({ signal }) => fetchSubsonicConnection(signal), select: (response) => response.data });
}

/** 确认密码：写请求不自动重试；成功后重读连接状态，状态从“需要确认”变成“已就绪”。 */
export function useConfirmSubsonicPassword() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    mutationFn: (password: string) => confirmSubsonicPassword(password),
    onSuccess: async () => {
      if (scope.isCurrent()) await queryClient.invalidateQueries({ queryKey: subsonicKeys.connection(scope.generation) });
    },
  });
}

/** 点唱机控制的播放区：即改即存，接口返回改后的连接状态，直接写回缓存。连续切换按顺序发送。 */
export function useSetJukeboxZone() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "subsonic-jukebox-zone" },
    mutationFn: (zoneId: string | null) => setJukeboxZone(zoneId),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = subsonicKeys.connection(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
    },
  });
}

export function useSubsonicSettings() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: subsonicKeys.settings(generation), queryFn: ({ signal }) => fetchSubsonicSettings(signal), select: (response) => response.data });
}

/** 实例开关：接口返回改后的状态，直接写回缓存；本人的连接状态随之失效。连续切换按顺序发送。 */
export function useSetSubsonicEnabled() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "subsonic-enabled" },
    mutationFn: (enabled: boolean) => setSubsonicEnabled(enabled),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = subsonicKeys.settings(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
      void queryClient.invalidateQueries({ queryKey: subsonicKeys.connection(scope.generation) });
    },
  });
}
