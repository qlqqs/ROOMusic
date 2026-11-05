import { Link } from "@tanstack/react-router";
import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { ArtistCommand, ArtistCommandResult, ArtistCommandState } from "../hooks/use-artist-commands";
import { artistFailureMessages } from "./governance-messages";

type ArtistCommandNoticeProps = {
  state: ArtistCommandState;
  /** 只显示与这位艺人有关的结果（作为目标或另一方）；null 是不限（候选页）。 */
  artistId: string | null;
  /** 失败由打开着的对话框显示时，这里不重复。 */
  hideFailure: boolean;
  /** 刚忽略的提示可以马上恢复（反向操作，不是撤销）。 */
  onUnignore?: ((command: ArtistCommand, result: ArtistCommandResult) => void) | undefined;
  onRetry: () => void;
  onDismiss: () => void;
};

function successText(command: ArtistCommand, result: ArtistCommandResult): string {
  switch (result.kind) {
    case "merge":
      return command.kind === "merge" ? `已把“${command.source.name}”并入“${command.artist.name}”。` : "已合并。";
    case "split":
      return command.kind === "split" ? `已拆出“${command.targetName}”。` : "已拆开。";
    case "rename":
      return result.result.name_source === "auto" ? `主名改回了最常见的写法：“${result.result.display_name}”。` : `主名已改为“${result.result.display_name}”。`;
    case "ignore":
      if (command.kind !== "ignore") return "已更新。";
      return result.result.ignored ? `以后不再提示“${command.artist.name}”和“${command.other.name}”可能是同一位。` : `已恢复“${command.artist.name}”和“${command.other.name}”的提示。`;
  }
}

function involves(command: ArtistCommand, artistId: string): boolean {
  if (command.artist.id === artistId) return true;
  if (command.kind === "merge") return command.source.id === artistId;
  if (command.kind === "ignore") return command.other.id === artistId;
  return false;
}

/**
 * 艺人治理的结果：成功、明确的拒绝与结果不确定各有不同的文案和下一步，就近显示在操作区，不用全局 toast。
 * 拆开成功后给出去新艺人页的链接；忽略提示成功后可以马上恢复。这些操作没有“撤销”。
 */
export function ArtistCommandNotice({ state, artistId, hideFailure, onUnignore, onRetry, onDismiss }: ArtistCommandNoticeProps) {
  if (state.status === "idle" || state.status === "pending") return null;
  if (artistId !== null && !involves(state.command, artistId)) return null;
  const dismiss = (
    <Button variant="text" onClick={onDismiss}>
      关闭
    </Button>
  );
  if (state.status === "succeeded") {
    const { command, result } = state;
    return (
      <Notice
        tone="success"
        action={
          <>
            {result.kind === "split" ? (
              <Link to="/artists/$artistId" params={{ artistId: result.result.target.id }}>
                {command.kind === "split" ? `打开“${command.targetName}”` : "打开新艺人"}
              </Link>
            ) : null}
            {result.kind === "ignore" && result.result.ignored && onUnignore !== undefined ? (
              <Button variant="text" onClick={() => onUnignore(command, result)}>
                恢复提示
              </Button>
            ) : null}
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
      <Notice tone="warning" title="艺人刚被修改过" action={dismiss}>
        页面已刷新。请看过之后再操作。
      </Notice>
    );
  }
  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: artistFailureMessages });
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
      {state.uncertain ? <p>页面已刷新。如果内容还是原来的样子，可以重试。</p> : null}
    </Notice>
  );
}

/** 对话框里的失败：与上面同一套文案，结果不确定时由对话框的主按钮变成“重试”。 */
export function ArtistDialogFailure({ state, kind }: { state: ArtistCommandState; kind: ArtistCommand["kind"] }) {
  if (state.status !== "failed" || state.command.kind !== kind) return null;
  if (!state.uncertain && isHttpFailure(state.failure, 409, "revision_conflict")) {
    return (
      <Notice tone="warning" title="艺人刚被修改过">
        页面已刷新。请再确认一次。
      </Notice>
    );
  }
  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: artistFailureMessages });
  return (
    <Notice tone="error" title={state.uncertain ? "结果未确认" : "没有完成"} requestId={description.requestId}>
      {description.message}
    </Notice>
  );
}
