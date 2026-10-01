import { Link } from "@tanstack/react-router";
import { Page } from "../../shared/ui";

/** 非管理员直接访问管理页时显示；管理接口不会被调用，后端也会独立拒绝。 */
export function ForbiddenPage() {
  return (
    <Page title="需要管理员权限" description="这个页面只有管理员可以使用。">
      <p>
        如果你需要管理资料库，请联系管理员。你可以 <Link to="/">回到概览</Link>。
      </p>
    </Page>
  );
}
