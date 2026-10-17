import { Link } from "@tanstack/react-router";
import type { Ref } from "react";
import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { ClaimResultDTO } from "../api/decoders";
import { claimResultText } from "../claim-text";
import type { ReleaseCommand, ReleaseCommandResult, ReleaseCommandState, RevertSubject } from "../hooks/use-release-commands";
import { correctionFieldLabels, reviewableIssueLabels } from "../labels";
import { governanceFailureMessages } from "./governance-messages";

type ReleaseCommandNoticeProps = {
  state: ReleaseCommandState;
  /** 只显示这个发行的结果：同一个页面换到别的专辑后，上一张专辑的结果不再出现。null 是不限发行（操作记录页）。 */
  releaseId: string | null;
  /** 失败由打开着的对话框显示时，这里不重复。 */
  hideFailure: boolean;
  /** 撤销刚刚成功的操作；只在成功提示里提供。没有提供（操作记录页只做撤销）时成功提示没有“撤销”。 */
  onUndo?: (command: ReleaseCommand, result: ReleaseCommandResult) => void;
  /** 用同一个幂等键重试上一个命令（只在结果不确定时提供）。 */
  onRetry: () => void;
  onDismiss: () => void;
  /**
   * 成功提示的焦点落点（tabIndex=-1，不进 Tab 顺序）。认领成功之后提交按钮随预览一起被换掉，焦点不能掉到 body：
   * 调用方在成功时把焦点放到这里。没有提供时成功提示不包这一层。
   */
  successFocusRef?: Ref<HTMLDivElement> | undefined;
};

const revertedText: Readonly<Record<RevertSubject, string>> = {
  correct: "已撤销这次修改，相关字段恢复到修改之前的状态。",
  join: "已撤销归组，这个版本回到了原来的专辑。",
  detach: "已撤销移出，这个版本回到了原来的专辑。",
  group: "已撤销这次归组调整，这个版本回到了原来的专辑。",
  default: "已撤销默认版本的调整。",
  review: "已撤销这次核对，问题恢复到之前的状态。",
  claim: "已撤销这次认领：修改还给了已不可见的旧版本，这个版本回到认领之前的样子。",
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
    case "default":
      return result.result.default_release_id === null
        ? "已改回自动选择：这张专辑默认显示年份最早的版本。"
        : "已把这个版本设为默认版本。专辑列表和打开这张专辑时会先显示它。";
    case "claim":
      return claimResultText(command.kind === "claim" ? command.source.title : "", command.release.title, result.result);
    case "review": {
      const label = reviewableIssueLabels[result.result.issue];
      return result.result.reviewed ? `已把“${label}”标记为已核对，它不再计入“需要关注”。扫描发现情况变了会重新提醒。` : `已取消“${label}”的核对，它重新计入“需要关注”。`;
    }
    case "revert":
      return revertedText[command.kind === "revert" ? command.subject : "other"];
  }
}

/** 认领成功之后“打开这张专辑”：新版本现在所在的专辑（认领可能把它归入了旧版本原来的专辑）。 */
function OpenClaimedAlbum({ command, result }: { command: ReleaseCommand; result: ClaimResultDTO }) {
  return (
    <Link to="/albums/$groupId" params={{ groupId: result.group_id }} search={{ release: command.release.id }}>
      打开这张专辑
    </Link>
  );
}

/**
 * 管理员写操作的结果（编辑、归组、默认版本、认领、撤销）：成功、明确的拒绝与结果不确定各有不同的文案和下一步。
 * 成功提示里有“撤销”（撤销本身的成功提示没有）；结果就近显示在操作区，不使用全局 toast。
 */
export function ReleaseCommandNotice({ state, releaseId, hideFailure, onUndo, onRetry, onDismiss, successFocusRef }: ReleaseCommandNoticeProps) {
  if (state.status === "idle" || (releaseId !== null && state.command.release.id !== releaseId)) return null;
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
    const notice = (
      <Notice
        tone="success"
        action={
          <>
            {result.kind === "claim" ? <OpenClaimedAlbum command={command} result={result.result} /> : null}
            {result.kind === "revert" || onUndo === undefined ? null : (
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
    return successFocusRef === undefined ? (
      notice
    ) : (
      <div ref={successFocusRef} tabIndex={-1}>
        {notice}
      </div>
    );
  }

  if (hideFailure) return null;
  if (!state.uncertain && isHttpFailure(state.failure, 409, "issue_changed")) {
    return (
      <Notice tone="warning" title="这个问题刚刚变了" action={dismiss}>
        扫描刚刚改变了这个问题，已重新读取，请看过之后再核对。
      </Notice>
    );
  }
  if (!state.uncertain && state.command.kind === "claim" && (isHttpFailure(state.failure, 409, "claim_changed") || isHttpFailure(state.failure, 409, "revision_conflict"))) {
    return (
      <Notice tone="warning" title="两个版本刚被改动过" action={dismiss}>
        预览之后又有扫描或修改，已重新读取。请看过新的预览再认领。
      </Notice>
    );
  }
  if (!state.uncertain && state.command.kind === "claim" && isHttpFailure(state.failure, 409, "not_claimable")) {
    return (
      <Notice tone="warning" title="这个旧版本现在不需要认领" action={dismiss}>
        它的文件回来了，或者它的修改已经认领给了别的版本。
      </Notice>
    );
  }
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
