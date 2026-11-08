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

/** 不扫描的子文件夹的增删在对话框里完成，列表本身就是结果，页面上不再提示。 */
function successText(result: RootCommandResult): { tone: "success" | "info" | "warning"; text: string } | null {
  switch (result.kind) {
    case "create":
      if (result.root.status === "disabled") {
        return { tone: "warning", text: `目录“${result.root.name}”已经添加过，现在是停用的。要扫描它，请先在列表里恢复。` };
      }
      return result.alreadyListed
        ? { tone: "info", text: `目录“${result.root.name}”已经添加过了。` }
        : { tone: "success", text: `已添加目录“${result.root.name}”，扫描后就能看到里面的专辑。` };
    case "disable":
      return { tone: "success", text: `已停用目录“${result.name}”，里面的曲目暂时不能播放，恢复后就能再播。` };
    case "restore":
      return { tone: "success", text: `已恢复目录“${result.name}”，里面的曲目可以播放了。` };
    case "exclude":
    case "unexclude":
      return null;
  }
}

/**
 * 目录命令的结果：成功、明确的拒绝（4xx）与结果不确定（连接中断、无效响应、5xx）各有不同的文案和下一步。
 * 路径被拒绝（invalid_library_path）显示在输入框旁，这里不重复。
 */
export function RootCommandNotice({ state, onRetry, onDismiss }: RootCommandNoticeProps) {
  const dismiss = (
    <Button onClick={onDismiss}>
      关闭
    </Button>
  );

  if (state.status === "succeeded") {
    const success = successText(state.result);
    return success === null ? null : (
      <Notice tone={success.tone} action={dismiss}>
        {success.text}
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
      {state.uncertain ? <p>列表已刷新。如果目录还是原来的状态，可以重试。</p> : null}
    </Notice>
  );
}
