import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { RefObject } from "react";
import type { ApiResponse } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { SettingsDTO } from "../api/decoders";
import {
  addAllowlistEntry,
  fetchAllowlist,
  fetchInstanceSettings,
  probeSettings,
  removeAllowlistEntry,
  testSearch,
  updateInstanceSettings,
  updateSettings,
  type SettingsPatch,
} from "../api/endpoints";
import { stewardKeys } from "../query-keys";
import { useStewardDisabledCheck } from "./use-run";

/** 保存与检测成功后，响应就是新的设置：直接写进设置查询，侧栏、标签栏、面板标题与输入框提示随之变化，不必重读。 */
function useStoreSettings() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return (response: ApiResponse<SettingsDTO>) => queryClient.setQueryData(stewardKeys.settings(generation), response);
}

/** 部分更新设置（PUT）。没有幂等键：失败后由用户决定是否再保存。得到 steward_disabled 时让设置查询失效，页面切到说明。 */
export function useUpdateSettings() {
  const store = useStoreSettings();
  const checkDisabled = useStewardDisabledCheck();
  return useMutation({ mutationFn: (patch: SettingsPatch) => updateSettings(patch), onSuccess: store, onError: (error) => checkDisabled(error) });
}

const probeMutationKey = ["steward", "probe"] as const;

/**
 * 检测（探测端点，成功即保存）。失败时错误的响应体里有本次的探测报告（decodeProbeFailure），原设置不变。
 * 密钥不进 mutation 的 variables（会留在 mutation 缓存里）：调用方把它放进 secretRef，mutationFn 取用，成功或失败后都清掉。
 */
export function useProbeSettings(secretRef: RefObject<string | null>) {
  const store = useStoreSettings();
  const checkDisabled = useStewardDisabledCheck();
  return useMutation({
    mutationKey: probeMutationKey,
    mutationFn: (input: { endpoint?: string; model?: string }) => {
      const key = secretRef.current;
      return probeSettings(key === null ? input : { ...input, key });
    },
    onSuccess: store,
    onError: (error) => checkDisabled(error),
    onSettled: () => {
      secretRef.current = null;
    },
  });
}

/** 正在检测：同一栏的保存、清除、修改与改名保存先停用，免得与检测的保存交错。 */
export function useProbePending(): boolean {
  return useIsMutating({ mutationKey: probeMutationKey }) > 0;
}

/**
 * 保存联网搜索的设置（C8，PUT）。Tavily 密钥与模型密钥一样不进 mutation 的 variables：调用方把它放进 secretRef，
 * mutationFn 取用并作为 search_key 提交，成功或失败后都清掉。
 */
export function useSaveSearchSettings(secretRef: RefObject<string | null>) {
  const store = useStoreSettings();
  const checkDisabled = useStewardDisabledCheck();
  return useMutation({
    mutationFn: (patch: Omit<SettingsPatch, "search_key">) => {
      const key = secretRef.current;
      return updateSettings(key === null ? patch : { ...patch, search_key: key });
    },
    onSuccess: store,
    onError: (error) => checkDisabled(error),
    onSettled: () => {
      secretRef.current = null;
    },
  });
}

/** 测试搜索（C8）：按已保存的设置请求一次；结果只显示在这一组里，不改设置。 */
export function useTestSearch() {
  const checkDisabled = useStewardDisabledCheck();
  return useMutation({ mutationFn: () => testSearch(), onError: (error) => checkDisabled(error) });
}

/** 管理员：成员开关。 */
export function useInstanceSettings() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: stewardKeys.instance(generation), queryFn: ({ signal }) => fetchInstanceSettings(signal), select: (response) => response.data });
}

export function useSetMembersEnabled() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: (enabled: boolean) => updateInstanceSettings({ members_enabled: enabled }),
    onSuccess: (response) => queryClient.setQueryData(stewardKeys.instance(generation), response),
    onError: () => queryClient.invalidateQueries({ queryKey: stewardKeys.instance(generation) }),
  });
}

/** 管理员：成员可以连接的内网地址。 */
export function useAllowlist() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: stewardKeys.allowlist(generation), queryFn: ({ signal }) => fetchAllowlist(signal), select: (response) => response.data.items });
}

export function useAddAllowlistEntry() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: (entry: { host: string; port: number }) => addAllowlistEntry(entry),
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.allowlist(generation) }),
  });
}

export function useRemoveAllowlistEntry() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: (entry: { host: string; port: number }) => removeAllowlistEntry(entry),
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.allowlist(generation) }),
  });
}
