import { PencilLine, Split, Combine } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import type { ReleaseCommand, ReleaseCommandResult, ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { DetachAlbumDialog } from "./detach-album-dialog";
import { EditReleaseDialog } from "./edit-release-dialog";
import { JoinAlbumDialog } from "./join-album-dialog";
import { ReleaseCommandNotice } from "./release-command-notice";
import styles from "./release-admin-actions.module.css";

type ReleaseAdminActionsProps = {
  /** 当前选中的版本；revision 是页面读到的人工修改版本号，归组与撤销用它做预期版本。 */
  release: ReleaseTarget & { revision: number };
  /** 版本现在所在的组，以及这个组里可见的版本数（只有一个版本时没有“移出为独立专辑”）。 */
  groupId: string;
  releaseCount: number;
  commands: ReleaseCommands;
};

/** 撤销刚刚成功的操作：撤销的目标与撤销之后的提示由命令的结果决定。 */
function undoIntent(command: ReleaseCommand, result: ReleaseCommandResult) {
  switch (result.kind) {
    case "correct":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: "correct" as const };
    case "join":
    case "detach":
      return { kind: "revert" as const, release: command.release, operationId: result.result.operation_id, subject: result.kind, returnGroupId: result.result.previous_group_id };
    case "revert":
      return null;
  }
}

/**
 * 专辑详情头部里管理员的操作区（只有管理员看到；后端仍独立鉴权）：编辑发行信息、归入其他专辑、移出为独立专辑，
 * 以及上一次操作的结果提示（成功时可以撤销）。命令的状态由页面持有，所以提示在导航到别的专辑组之后仍能显示（只显示这个版本相关的结果）；
 * 版本换了专辑组之后页面怎么跟过去，由命令 hook 的 onMoved（页面提供）负责。
 */
export function ReleaseAdminActions({ release, groupId, releaseCount, commands }: ReleaseAdminActionsProps) {
  const [dialog, setDialog] = useState<"edit" | "join" | "detach" | null>(null);
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
        <Button icon={<Combine size={16} />} onClick={() => open("join")}>
          归入其他专辑…
        </Button>
        {releaseCount > 1 ? (
          <Button icon={<Split size={16} />} onClick={() => open("detach")}>
            移出为独立专辑
          </Button>
        ) : null}
      </div>
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
      <DetachAlbumDialog release={dialog === "detach" ? release : null} commands={commands} onClose={close} />
    </div>
  );
}
