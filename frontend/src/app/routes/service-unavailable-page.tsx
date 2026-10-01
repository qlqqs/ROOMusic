import { describeFailure, type ApiFailure } from "../../shared/api";
import { Button, Notice } from "../../shared/ui";
import { AuthLayout } from "./auth-layout";

/** 启动时无法确认服务或会话（例如 503）：整页错误，可重试。 */
export function ServiceUnavailablePage({ failure, onRetry }: { failure: ApiFailure; onRetry: () => void }) {
  const description = describeFailure(failure);
  return (
    <AuthLayout title="暂时无法使用" description="没能连接到 ROOMusic 服务。">
      <Notice tone="error" requestId={description.requestId} action={<Button onClick={onRetry}>重试</Button>}>
        {description.message}
      </Notice>
    </AuthLayout>
  );
}
