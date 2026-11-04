import { Link } from "@tanstack/react-router";
import { Page } from "../../shared/ui";

export function NotFoundPage() {
  return (
    <Page title="页面不存在">
      <p>
        请检查地址是否正确，或 <Link to="/">回到概览</Link>。
      </p>
    </Page>
  );
}
