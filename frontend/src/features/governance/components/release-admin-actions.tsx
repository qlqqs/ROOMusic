import { Link } from "@tanstack/react-router";
import { ArrowRightLeft, Bookmark, ClipboardCheck, Combine, PencilLine, RotateCcw, Split } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import { useClaimHints } from "../hooks/use-claims";
import type { ReleaseCommand, ReleaseCommandResult, ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { DetachAlbumDialog } from "./detach-album-dialog";
import { EditReleaseDialog } from "./edit-release-dialog";
import { JoinAlbumDialog } from "./join-album-dialog";
import { ReleaseCommandNotice } from "./release-command-notice";
import styles from "./release-admin-actions.module.css";

type ReleaseAdminActionsProps = {
  /** 当前选中的版本；revision 是页面读到的人工修改版本号，归组与撤销用它做预期版本；attentionCount 大于 0 时有“查看问题”。 */
  release: ReleaseTarget & { revision: number; attentionCount: number };
  /** 版本现在所在的组，以及这个组里可见的版本数（只有一个版本时没有“移出为独立专辑”与默认版本的按钮）。 */
  groupId: string;
  releaseCount: number;
  /** 专辑页面读到的人工修改版本号（设定与清除默认版本时作为预期版本）与生效中的默认版本（没有时为 null，专辑按自动规则显示）。 */
  groupRevision: number;
  defaultReleaseId: string | null;
  commands: ReleaseCommands;
};

/** 撤销刚刚成功的操作：撤销的目标与撤销之后的提示由命令的结果决定。问题页的核对也用它。 */
export function undoIntent(command: ReleaseCommand, result: ReleaseCommandResult) {
  switch (result.kind) {
    case "correct":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: "correct" as const };
    case "join":
    case "detach":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: result.kind, returnGroupId: result.result.previous_group_id };
    case "default":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: "default" as const, returnGroupId: result.result.group_id };
    case "review":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: "review" as const };
    case "claim":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: "claim" as const };
    case "revert":
      return null;
  }
}

/**
 * 专辑详情头部里管理员的操作区（只有管理员看到；后端仍独立鉴权）：编辑发行信息、设为默认版本或改回自动选择（多于一个版本时）、
 * 归入其他专辑、移出为独立专辑、这个版本需要关注时的“查看问题”（打开问题页并选中它），以及上一次操作的结果提示（成功时可以撤销）。设定与清除默认版本不弹确认（随时可以撤销，也不丢数据），
 * 自动选出的代表版本也可以“设为默认版本”：设定之后，以后归入更早的版本也不会把它挤下去。命令的状态由页面持有，
 * 所以提示在导航到别的专辑组之后仍能显示（只显示这个版本相关的结果）；页面怎么跟到版本现在的位置（换了专辑组，或设定默认版本之后
 * 代表版本变了），由命令 hook 的 onMoved（页面提供）负责。
 */
export function ReleaseAdminActions({ release, groupId, releaseCount, groupRevision, defaultReleaseId, commands }: ReleaseAdminActionsProps) {
  const [dialog, setDialog] = useState<"edit" | "join" | "detach" | null>(null);
  // 提示只是线索：读取中、失败或没有时什么也不显示，不打扰页面。
  const hints = useClaimHints(release.id).data ?? [];
  const isDefault = release.id === defaultReleaseId;
  const defaultPending = commands.state.status === "pending" && commands.state.command.kind === "default";
  const open = (next: "edit" | "join" | "detach") => {
    commands.dismiss();
    setDialog(next);
  };
  const close = () => setDialog(null);
  return (
    <div className={styles.admin}>
      <div className={styles.row}>
        <Button icon={<PencilLine size={16} />} onClick={() => open("edit")}>
          编辑发行信息
        </Button>
        {releaseCount > 1 ? (
          <Button
            icon={isDefault ? <RotateCcw size={16} /> : <Bookmark size={16} />}
            busy={defaultPending}
            onClick={() => {
              commands.dismiss();
              commands.submit({ kind: "default", release, group: { id: groupId, revision: groupRevision }, target: isDefault ? null : release.id });
            }}
          >
            {defaultPending ? "正在保存…" : isDefault ? "改回自动选择" : "设为默认版本"}
          </Button>
        ) : null}
        <Button icon={<Combine size={16} />} onClick={() => open("join")}>
          归入其他专辑…
        </Button>
        {releaseCount > 1 ? (
          <Button icon={<Split size={16} />} onClick={() => open("detach")}>
            移出为独立专辑
          </Button>
        ) : null}
        {release.attentionCount > 0 ? (
          <Link to="/issues" search={{ release: release.id }} className={styles.link}>
            <ClipboardCheck size={16} aria-hidden="true" /> 查看问题
          </Link>
        ) : null}
      </div>
      {hints.length === 0 ? null : (
        <ul className={styles.hints} aria-label="可能接替了已不可见的版本">
          {hints.map((hint) => (
            <li key={hint.id} className={styles.hint}>
              <ArrowRightLeft size={16} aria-hidden="true" />
              <span>可能接替了已不可见的《{hint.title}》</span>
              <Link to="/claims" search={{ release: hint.id, target: release.id }} aria-label={`查看并认领《${hint.title}》，它可能被这个版本接替了`}>
                查看并认领
              </Link>
            </li>
          ))}
        </ul>
      )}
      <ReleaseCommandNotice
        state={commands.state}
        releaseId={release.id}
        hideFailure={dialog !== null}
        onUndo={(command, result) => {
          const intent = undoIntent(command, result);
          if (intent !== null) commands.submit(intent);
        }}
        onRetry={() => commands.retry()}
        onDismiss={commands.dismiss}
      />
      <EditReleaseDialog open={dialog === "edit"} release={release} commands={commands} onClose={close} />
      <JoinAlbumDialog open={dialog === "join"} release={release} currentGroupId={groupId} commands={commands} onClose={close} />
      <DetachAlbumDialog release={dialog === "detach" ? release : null} isDefault={isDefault} commands={commands} onClose={close} />
    </div>
  );
}
