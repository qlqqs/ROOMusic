import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { ReleaseCommand, ReleaseCommandResult, ReleaseCommandState, RevertSubject } from "../hooks/use-release-commands";
import { correctionFieldLabels } from "../labels";
import { governanceFailureMessages } from "./governance-messages";

type ReleaseCommandNoticeProps = {
  state: ReleaseCommandState;
  /** 只显示这个发行的结果：同一个页面换到别的专辑后，上一张专辑的结果不再出现。 */
  releaseId: string;
  /** 失败由打开着的对话框显示时，这里不重复。 */
  hideFailure: boolean;
  /** 撤销刚刚成功的操作；只在成功提示里提供。 */
  onUndo: (command: ReleaseCommand, result: ReleaseCommandResult) => void;
  /** 用同一个幂等键重试上一个命令（只在结果不确定时提供）。 */
  onRetry: () => void;
  onDismiss: () => void;
};

const revertedText: Readonly<Record<RevertSubject, string>> = {
  correct: "已撤销这次修改，相关字段恢复到修改之前的状态。",
  join: "已撤销归组，这个版本回到了原来的专辑。",
  detach: "已撤销移出，这个版本回到了原来的专辑。",
  other: "已撤销。",
};

function successText(command: ReleaseCommand, result: ReleaseCommandResult): string {
  switch (result.kind) {
    case "correct":
      return `已保存修改：${(command.kind === "correct" ? command.fields : []).map((field) => correctionFieldLabels[field]).join("、")}。重新扫描不会覆盖它们。`;
    case "join":
      return `已把这个版本归入《${result.groupTitle}》。`;
    case "detach":
      return "已把这个版本移出为独立专辑。";
    case "revert":
      return revertedText[command.kind === "revert" ? command.subject : "other"];
  }
}

/**
 * 管理员写操作的结果（编辑、归组、撤销）：成功、明确的拒绝与结果不确定各有不同的文案和下一步。
 * 成功提示里有“撤销”（撤销本身的成功提示没有）；结果就近显示在操作区，不使用全局 toast。
 */
export function ReleaseCommandNotice({ state, releaseId, hideFailure, onUndo, onRetry, onDismiss }: ReleaseCommandNoticeProps) {
  if (state.status === "idle" || state.command.release.id !== releaseId) return null;
  const dismiss = (
    <Button variant="text" onClick={onDismiss}>
      关闭
    </Button>
  );

  if (state.status === "pending") {
    return state.command.kind === "revert" ? <Notice>正在撤销…</Notice> : null;
  }

  if (state.status === "succeeded") {
    const { command, result } = state;
    return (
      <Notice
        tone="success"
        action={
          <>
            {result.kind === "revert" ? null : (
              <Button variant="text" onClick={() => onUndo(command, result)}>
                撤销
              </Button>
            )}
            {dismiss}
          </>
        }
      >
        {successText(command, result)}
      </Notice>
    );
  }

  if (hideFailure) return null;
  if (!state.uncertain && isHttpFailure(state.failure, 409, "revision_conflict")) {
    return (
      <Notice tone="warning" title="这张专辑刚被修改过" action={dismiss}>
        页面已刷新。请核对当前内容后再重新操作。
      </Notice>
    );
  }
  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: governanceFailureMessages });
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
      {state.uncertain ? <p>页面已刷新。如果内容仍是原来的样子，可以重试；重试沿用同一个操作标识，不会重复执行。</p> : null}
    </Notice>
  );
}
