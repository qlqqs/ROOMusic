import { AccessTokens } from "../../features/steward";
import { Page } from "../../shared/ui";

/** 设置 › 访问令牌：让 MCP 客户端以自己的身份使用 ROOMusic 的工具。所有登录用户都有。 */
export function TokensPage() {
  return (
    <Page title="访问令牌" description="访问令牌让 Claude Code 等 MCP 客户端以你的身份使用 ROOMusic 的工具，权限不会超过你自己的账号。">
      <AccessTokens />
    </Page>
  );
}
