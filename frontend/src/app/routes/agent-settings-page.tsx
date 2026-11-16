import { AgentSettings } from "../../features/steward";
import { Page } from "../../shared/ui";

/** 设置 › Agent：管家的名字、模型、访问令牌；管理员另有成员开关与内网地址放行。所有登录用户都有。 */
export function AgentSettingsPage() {
  return (
    <Page title="Agent">
      <AgentSettings />
    </Page>
  );
}
