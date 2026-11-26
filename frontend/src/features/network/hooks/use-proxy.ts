import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { MutableRefObject } from "react";
import { useViewerScope } from "../../auth";
import type { ProxyDraft } from "../api/decoders";
import { fetchProxySettings, saveProxySettings, testProxySettings } from "../api/endpoints";
import { networkKeys } from "../query-keys";

export function useProxySettings() {
  const { generation } = useViewerScope();
  return useQuery({ queryKey: networkKeys.proxy(generation), queryFn: ({ signal }) => fetchProxySettings(signal), select: (response) => response.data });
}

/**
 * 保存：接口返回保存后的完整设置，直接写回缓存。
 * 密码不进 mutation 的 variables（开发工具与缓存里看得到 variables），提交前放进 ref，发送时取出并清空。
 */
export function useSaveProxySettings(passwordRef: MutableRefObject<string | null>) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  return useMutation({
    scope: { id: "network-proxy" },
    mutationFn: ({ draft, version }: { draft: Omit<ProxyDraft, "password">; version: number }) => {
      const secret = passwordRef.current;
      passwordRef.current = null;
      return saveProxySettings(secret === null ? draft : { ...draft, password: secret }, version);
    },
    onSuccess: async (response) => {
      if (!scope.isCurrent()) return;
      const key = networkKeys.proxy(scope.generation);
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, response);
    },
  });
}

/** 测试连接：同样不把密码放进 variables。 */
export function useTestProxy(passwordRef: MutableRefObject<string | null>) {
  return useMutation({
    mutationFn: (draft: Omit<ProxyDraft, "enabled" | "password">) => {
      const secret = passwordRef.current;
      passwordRef.current = null;
      return testProxySettings(secret === null ? draft : { ...draft, password: secret });
    },
  });
}
