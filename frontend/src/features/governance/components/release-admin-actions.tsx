import { PencilLine } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import type { ReleaseCommandResult, ReleaseCommands, ReleaseCommand, ReleaseTarget } from "../hooks/use-release-commands";
import { EditReleaseDialog } from "./edit-release-dialog";
import { ReleaseCommandNotice } from "./release-command-notice";
import styles from "./release-admin-actions.module.css";

type ReleaseAdminActionsProps = {
  /** 当前选中的版本。 */
  release: ReleaseTarget;
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
 * 专辑详情头部里管理员的操作区（只有管理员看到；后端仍独立鉴权）：编辑发行信息，以及上一次操作的结果提示。
 * 命令的状态由页面持有，所以提示在导航到别的专辑组之后仍能显示（只显示这个版本相关的结果）。
 */
export function ReleaseAdminActions({ release, commands }: ReleaseAdminActionsProps) {
  const [editing, setEditing] = useState(false);
  return (
    <div className={styles.admin}>
      <div className={styles.row}>
        <Button
          icon={<PencilLine size={16} />}
          onClick={() => {
            commands.dismiss();
            setEditing(true);
          }}
        >
          编辑发行信息
        </Button>
      </div>
      <ReleaseCommandNotice
        state={commands.state}
        releaseId={release.id}
        hideFailure={editing}
        onUndo={(command, result) => {
          const intent = undoIntent(command, result);
          if (intent !== null) commands.submit(intent);
        }}
        onRetry={() => commands.retry()}
        onDismiss={commands.dismiss}
      />
      <EditReleaseDialog open={editing} release={release} commands={commands} onClose={() => setEditing(false)} />
    </div>
  );
}
