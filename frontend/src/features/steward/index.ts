// steward 的公开入口（轻量部分，随外壳一起加载）：入口与名字、面板状态、登出时的清理，以及设置页的 Agent 栏目（由设置路由按需加载）。
// 对话界面（面板、整页、Markdown）在 ./chat，由外壳与路由按需加载。
import { resetDock } from "./dock-store";
import { resetRuns } from "./run/run-store";

export { AgentSettings } from "./settings/agent-settings";
export { preferBlankConversation, setDockOpen, toggleDock, useDockState } from "./dock-store";
export { DEFAULT_STEWARD_NAME, useStewardEntry } from "./hooks/use-steward-settings";

/** 登出或换用户：中止进行中的回答，清掉面板里的会话、草稿与结束信息。 */
export function resetStewardState(): void {
  resetRuns();
  resetDock();
}
