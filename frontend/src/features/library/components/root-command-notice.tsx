import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { RootCommandResult, RootCommandState } from "../hooks/use-root-commands";
import { rootFailureMessages } from "./root-messages";

type RootCommandNoticeProps = {
  state: RootCommandState;
  /** 用同一个幂等键重试上一个命令（只在结果不确定时提供）。 */
  onRetry: () => void;
  onDismiss: () => void;
};

function successText(result: RootCommandResult): { tone: "success" | "info" | "warning"; text: string } {
  switch (result.kind) {
    case "create":
      if (result.root.status === "disabled") {
        return { tone: "warning", text: `目录“${result.root.name}”已经登记，当前处于停用状态，没有自动恢复。需要扫描它时，请先在列表中恢复。` };
      }
      return result.alreadyListed
        ? { tone: "info", text: `目录“${result.root.name}”已经登记，列表没有变化。` }
        : { tone: "success", text: `已添加目录“${result.root.name}”。下一次扫描会包含它。` };
    case "disable":
      return { tone: "success", text: `已停用目录“${result.name}”。扫描会跳过它，已入库的内容保留。` };
    case "restore":
      return { tone: "success", text: `已恢复目录“${result.name}”。下一次扫描会包含它。` };
  }
}

/**
 * 目录命令的结果：成功、明确的拒绝（4xx）与结果不确定（连接中断、无效响应、5xx）各有不同的文案和下一步。
 * 路径被拒绝（invalid_library_path）显示在输入框旁，这里不重复。
 */
export function RootCommandNotice({ state, onRetry, onDismiss }: RootCommandNoticeProps) {
  const dismiss = (
    <Button variant="text" onClick={onDismiss}>
      关闭
    </Button>
  );

  if (state.status === "succeeded") {
    const { tone, text } = successText(state.result);
    return (
      <Notice tone={tone} action={dismiss}>
        {text}
      </Notice>
    );
  }

  if (state.status !== "failed") return null;
  if (state.command.kind === "create" && isHttpFailure(state.failure, 400, "invalid_library_path")) return null;

  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: rootFailureMessages });
  return (
    <Notice
      tone="error"
      title={state.uncertain ? "结果未确认" : "操作没有完成"}
      requestId={description.requestId}
      action={
        <>
          {state.uncertain ? <Button onClick={onRetry}>重试</Button> : null}
          {dismiss}
        </>
      }
    >
      <p>{description.message}</p>
      {state.uncertain ? <p>列表已刷新。如果目录仍是原来的状态，可以重试；重试沿用同一个操作标识，不会重复执行。</p> : null}
    </Notice>
  );
}
