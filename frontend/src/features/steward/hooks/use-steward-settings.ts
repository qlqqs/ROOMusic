import { queryOptions, useQuery } from "@tanstack/react-query";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { SettingsDTO } from "../api/decoders";
import { fetchSettings } from "../api/endpoints";
import { stewardKeys } from "../query-keys";

/** 没起名字时的名字。 */
export const DEFAULT_STEWARD_NAME = "管家";

export function settingsQueryOptions(generation: number) {
  return queryOptions({
    queryKey: stewardKeys.settings(generation),
    queryFn: ({ signal }) => fetchSettings(signal),
    select: (response) => response.data,
  });
}

/** 自己的管家设置。成员开关关闭时成员得到 403 steward_disabled。 */
export function useStewardSettings() {
  const { generation } = useViewerScope();
  return useQuery(settingsQueryOptions(generation));
}

/**
 * 能不能对话（合同 4.2 的“已配置”与工具能力）：
 * ready 可以发消息；not_configured 缺端点、协议或模型；needs_key 已存密钥解不开；no_tool_support 模型不支持调用工具。
 */
export type StewardReadiness = "ready" | "not_configured" | "needs_key" | "no_tool_support";

export function readinessOf(settings: SettingsDTO): StewardReadiness {
  if (settings.key_needs_reentry) return "needs_key";
  const protocol = settings.overrides.protocol ?? settings.protocol;
  if (settings.endpoint === "" || protocol === "" || effectiveModelOf(settings) === "") return "not_configured";
  const toolSupport = settings.overrides.tool_support ?? settings.probe_result?.tool_support ?? "unconfirmed";
  return toolSupport === "unsupported" ? "no_tool_support" : "ready";
}

/** 设置里生效的对话模型（覆盖项优先）。 */
export function effectiveModelOf(settings: SettingsDTO): string {
  return settings.overrides.model ?? settings.model;
}

/**
 * 入口与名字：设置读到了才显示入口（成员开关关闭时是 403，读取失败或还没读到时也不显示），
 * 名字没起时是“管家”。侧栏、标签栏、面板标题与输入框提示都取这里，改名后随设置查询一起变化。
 * unread 是运行结束后还没打开看过的会话数（C10），大于 0 时入口有提示点；随设置查询在运行结束、打开会话、窗口重新聚焦时重读。
 */
export function useStewardEntry(): { available: boolean; name: string; unread: number } {
  const query = useStewardSettings();
  const settings = query.data;
  return {
    available: settings !== undefined && !isStewardDisabled(query.error),
    name: settings === undefined || settings.display_name === "" ? DEFAULT_STEWARD_NAME : settings.display_name,
    unread: settings?.unread_conversation_count ?? 0,
  };
}

/** 管理员关闭了成员使用管家（之前读到过设置、之后重读时被拒绝也算）。 */
export function isStewardDisabled(error: unknown): boolean {
  return isHttpFailure(failureOf(error), 403, "steward_disabled");
}

/**
 * 本人的对话模型与审查模型（自动整理的设置里用：整理用的模型为空时用对话模型；审查模型与它相同时提示另选一个，U11）。
 * 没读到设置或管家被关闭时是 undefined；审查模型为空表示与对话同一个模型。
 */
export function useStewardModels(): { model: string; reviewModel: string } | undefined {
  const settings = useStewardSettings().data;
  if (settings === undefined) return undefined;
  const model = effectiveModelOf(settings);
  return { model, reviewModel: settings.review_model === undefined || settings.review_model === "" ? model : settings.review_model };
}
