import { InsightsSettings } from "../../features/insights";
import { Page } from "../../shared/ui";

/** 设置 › 匿名统计：开关、上次发送与将要发送的完整内容。管理员专用，由路由层的 AdminGate 把关。 */
export function InsightsPage() {
  return (
    <Page title="匿名统计" description="帮助项目了解有多少实例在用、用到了哪些功能；只发分档后的数字与功能开关。">
      <InsightsSettings />
    </Page>
  );
}
