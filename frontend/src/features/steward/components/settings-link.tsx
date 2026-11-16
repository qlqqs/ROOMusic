import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

/** 设置页的 Agent 栏目（名字、模型、访问令牌）。 */
export const AGENT_SETTINGS_PATH = "/settings/agent";

/** 去 Agent 设置的站内链接：在应用里导航，不重新载入页面（进行中的回答不中断）。 */
export function AgentSettingsLink({ className, children }: { className?: string | undefined; children: ReactNode }) {
  return (
    <Link to={AGENT_SETTINGS_PATH} className={className}>
      {children}
    </Link>
  );
}
