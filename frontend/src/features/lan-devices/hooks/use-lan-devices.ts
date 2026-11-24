import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { CastFormatDTO, LanDeviceDTO, LanDeviceSettingsDTO } from "../api/decoders";
import { addLanDevice, fetchLanDevices, fetchLanDeviceSettings, putCastFormat, putLanDeviceGrants, removeLanDevice, updateLanDeviceSettings } from "../api/endpoints";
import { lanDeviceKeys } from "../query-keys";

/** 发现到的设备每 30 秒刷新一次（后端每 30 秒搜索一次）；开关关着时不轮询。 */
const DEVICE_REFRESH_MS = 30_000;

export function useLanDeviceSettings() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: lanDeviceKeys.settings(generation), queryFn: ({ signal }) => fetchLanDeviceSettings(signal), select: (response) => response.data });
}

/** 改开关：写回设置；设备列表与播放器里的设备都跟着变，一起刷新。连续切换按顺序发送。 */
export function useUpdateLanDeviceSettings() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "lan-device-settings" },
    mutationFn: (change: Partial<LanDeviceSettingsDTO>) => updateLanDeviceSettings(change),
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = lanDeviceKeys.settings(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
      void queryClient.invalidateQueries({ queryKey: lanDeviceKeys.devices(scope.generation) });
    },
  });
}

export function useLanDevices(enabled: boolean) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: lanDeviceKeys.devices(generation),
    queryFn: ({ signal }) => fetchLanDevices(signal),
    select: (response) => response.data.items,
    refetchInterval: enabled ? DEVICE_REFRESH_MS : false,
  });
}

function useRefreshDevices() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return () => {
    if (scope.isCurrent()) void queryClient.invalidateQueries({ queryKey: lanDeviceKeys.devices(scope.generation) });
  };
}

export function useAddLanDevice() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ kind, address }: { kind: LanDeviceDTO["kind"]; address: string }) => addLanDevice(kind, address), onSuccess: refresh });
}

export function useRemoveLanDevice() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: (udn: string) => removeLanDevice(udn), onSuccess: refresh });
}

export function usePutLanDeviceGrants() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ udn, userIds }: { udn: string; userIds: readonly string[] }) => putLanDeviceGrants(udn, userIds), onSuccess: refresh });
}

/** 改一台 Google Cast 设备的编码设置；成功后刷新设备列表（下一首起生效）。 */
export function usePutCastFormat() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ udn, format }: { udn: string; format: CastFormatDTO }) => putCastFormat(udn, format), onSuccess: refresh });
}
