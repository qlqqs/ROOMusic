import { MemberManagement } from "../../features/members";
import { Page } from "../../shared/ui";

/** 成员管理：新建、停用、重新启用与撤销会话。管理员专用，由路由层的 AdminGate 把关。 */
export function MembersPage() {
  return (
    <Page title="成员">
      <MemberManagement />
    </Page>
  );
}
