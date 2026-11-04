import { formatDateTime } from "../../../shared/lib/time";
import { ConfirmDialog, FailureNotice } from "../../../shared/ui";
import type { OperationItemDTO } from "../api/decoders";
import { operationTypeLabel, revertLines } from "../change-summary";
import type { ReleaseCommands, ReleaseIntent, ReleaseTarget } from "../hooks/use-release-commands";
import { governanceFailureMessages } from "./governance-messages";
import styles from "./revert-operation-dialog.module.css";

type RevertIntent = Extract<ReleaseIntent, { kind: "revert" }>;

type RevertOperationDialogProps = {
  /** 要撤销的记录；null 表示对话框关闭。 */
  item: OperationItemDTO | null;
  /**
   * 页面正在显示的版本（专辑详情里的“修改记录”）：撤销专辑目标的记录（默认版本）之后页面继续显示它。
   * 操作记录页没有“当前版本”，不传：它不跟随导航，命令的目标就是这张专辑（只用来写提示）。
   */
  current?: ReleaseTarget | undefined;
  commands: ReleaseCommands;
  onClose: () => void;
};

/**
 * 撤销这条记录的命令。归组调整撤销之后版本回到原来的专辑（记录里的原组），页面据此跟过去；操作记录页不跟随。
 * 目标是专辑的记录（默认版本）撤销之后专辑不变，页面继续显示当前版本。
 */
function revertIntentOf(item: OperationItemDTO, current: ReleaseTarget | undefined): RevertIntent | null {
  if (item.release === null && item.album !== null) {
    return { kind: "revert", release: current ?? { id: item.album.id, title: item.album.title }, operationId: item.id, subject: "default", returnGroupId: item.album.id };
  }
  if (item.release === null) return null;
  const release = { id: item.release.id, title: item.release.title };
  if (item.changes?.kind === "release_group_change") {
    return { kind: "revert", release, operationId: item.id, subject: "group", returnGroupId: item.changes.from.group_id };
  }
  if (item.changes?.kind === "release_issue_review") return { kind: "revert", release, operationId: item.id, subject: "review" };
  if (item.changes?.kind === "release_claim") {
    return { kind: "revert", release, operationId: item.id, subject: "claim", returnGroupId: item.changes.previous_group_id };
  }
  return { kind: "revert", release, operationId: item.id, subject: item.changes === null ? "other" : "correct" };
}

const titles = {
  release_correction: "撤销这次修改？",
  release_group_change: "撤销这次归组调整？",
  release_group_default: "撤销这次默认版本调整？",
  release_issue_review: "撤销这次核对？",
  release_claim: "撤销这次认领？",
} as const;

/**
 * 撤销一条记录前确认，并写明会改回什么：撤销只恢复这一条记录改动的内容，也会留下一条自己的记录（它不能再被撤销，需要时重新修改）。
 * 结果不确定时对话框保持打开，“确认”变成用同一个幂等键“重试撤销”；成功或明确的拒绝（之后又有修改、已撤销……）关闭对话框，
 * 由页面上的提示说明结果，列表随刷新显示最新的“能不能撤销”。
 */
export function RevertOperationDialog({ item, current, commands, onClose }: RevertOperationDialogProps) {
  const { state } = commands;
  const pending = state.status === "pending" && state.command.kind === "revert";
  const uncertainFailure = state.status === "failed" && state.command.kind === "revert" && state.uncertain ? state : null;

  const callbacks = {
    onSucceeded: onClose,
    onFailed: (_failure: unknown, uncertain: boolean) => {
      if (!uncertain) onClose();
    },
  };

  return (
    <ConfirmDialog
      open={item !== null}
      title={item === null || item.type === "revert" ? "撤销这条操作？" : titles[item.type]}
      confirmLabel={uncertainFailure === null ? "撤销" : "重试撤销"}
      pending={pending}
      error={
        uncertainFailure === null ? undefined : (
          <FailureNotice error={uncertainFailure.error} context="write" messages={governanceFailureMessages} title="结果未确认" />
        )
      }
      onConfirm={() => {
        if (item === null) return;
        if (uncertainFailure !== null) {
          commands.retry(callbacks);
          return;
        }
        const intent = revertIntentOf(item, current);
        if (intent !== null) commands.submit(intent, callbacks);
      }}
      onCancel={() => {
        commands.dismiss();
        onClose();
      }}
    >
      {item === null ? null : (
        <>
          <p>
            撤销 {item.actor.username} 在 {formatDateTime(item.created_at)} 做的“{operationTypeLabel(item)}”。《{(item.release ?? item.album)?.title ?? ""}》会改回：
          </p>
          <ul className={styles.lines}>
            {revertLines(item).map((line, index) => (
              <li key={index}>{line}</li>
            ))}
          </ul>
          <p>撤销之后不能再撤销，需要时可以重新修改。</p>
        </>
      )}
    </ConfirmDialog>
  );
}
