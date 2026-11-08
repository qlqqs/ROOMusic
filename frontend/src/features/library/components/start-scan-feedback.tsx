import { Link } from "@tanstack/react-router";
import { Button, FailureNotice, Notice } from "../../../shared/ui";
import type { useBeginScan } from "../hooks/use-scan-commands";
import { scanFailureMessages } from "./root-messages";

type StartScanFeedbackProps = Pick<ReturnType<typeof useBeginScan>, "start" | "begin">;

/** “开始扫描”的结果：失败时可以重试；已有扫描在运行（200）时提示并链接到那一个。新建的扫描已经直接打开，这里不显示。 */
export function StartScanFeedback({ start, begin }: StartScanFeedbackProps) {
  return (
    <>
      {start.isError ? (
        <FailureNotice
          error={start.error}
          context="write"
          messages={scanFailureMessages}
          action={<Button onClick={begin}>重试</Button>}
        />
      ) : null}
      {start.isSuccess && start.data.status === 200 ? (
        <Notice tone="info">
          已经有扫描在运行。{" "}
          <Link to="/settings/library/scans/$scanId" params={{ scanId: start.data.data.id }}>
            查看这个扫描
          </Link>
          。
        </Notice>
      ) : null}
    </>
  );
}
