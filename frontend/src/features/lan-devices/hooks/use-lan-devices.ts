import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { AirPlayFormatDTO, CastFormatDTO, LanDeviceDTO, LanDeviceSettingsChange, SlimProtoFormatDTO } from "../api/decoders";
import { addLanDevice, fetchLanDevices, fetchLanDeviceSettings, finishAirPlayPairing, putAirPlayFormat, putCastFormat, putSlimProtoFormat, startAirPlayPairing, putLanDeviceGrants, removeLanDevice, updateLanDeviceSettings } from "../api/endpoints";
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
    mutationFn: (change: LanDeviceSettingsChange) => updateLanDeviceSettings(change),
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

export function usePutAirPlayFormat() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ udn, format }: { udn: string; format: AirPlayFormatDTO }) => putAirPlayFormat(udn, format), onSuccess: refresh });
}

/** 改一台 SlimProto 播放器的音质；采样率下一条流起生效，音量方式立即生效。 */
export function usePutSlimProtoFormat() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ udn, format }: { udn: string; format: SlimProtoFormatDTO }) => putSlimProtoFormat(udn, format), onSuccess: refresh });
}

export function useStartAirPlayPairing() {
  return useMutation({ mutationFn: (udn: string) => startAirPlayPairing(udn) });
}

/** 配对成功后刷新设备列表（paired 变为 true）。 */
export function useFinishAirPlayPairing() {
  const refresh = useRefreshDevices();
  return useMutation({ mutationFn: ({ udn, pin }: { udn: string; pin: string }) => finishAirPlayPairing(udn, pin), onSuccess: refresh });
}
