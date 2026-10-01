import { FailureNotice, ConfirmDialog } from "../../../shared/ui";
import type { RootCommands, RootTarget } from "../hooks/use-root-commands";
import { rootFailureMessages } from "./root-messages";

type DisableRootDialogProps = {
  /** 要停用的目录；null 表示对话框关闭。 */
  root: RootTarget | null;
  commands: RootCommands;
  onClose: () => void;
};

/**
 * 停用前确认：说明影响范围（扫描跳过、已入库内容保留且不会被标为缺失、可恢复）。
 * 结果不确定时对话框保持打开并在其中显示错误，“确认”变成用同一个幂等键“重试”；
 * 成功或明确的拒绝（例如目录已变化的 409）则关闭对话框，由页面上的提示说明结果。
 */
export function DisableRootDialog({ root, commands, onClose }: DisableRootDialogProps) {
  const { state } = commands;
  const pending = state.status === "pending";
  const uncertainFailure = state.status === "failed" && state.uncertain ? state : null;

  const callbacks = {
    onSucceeded: onClose,
    onFailed: (_failure: unknown, uncertain: boolean) => {
      if (!uncertain) onClose();
    },
  };

  return (
    <ConfirmDialog
      open={root !== null}
      title={`停用目录“${root?.name ?? ""}”？`}
      confirmLabel={uncertainFailure === null ? "停用目录" : "重试停用"}
      pending={pending}
      error={
        uncertainFailure === null ? undefined : (
          <FailureNotice error={uncertainFailure.error} context="write" messages={rootFailureMessages} title="结果未确认" />
        )
      }
      onConfirm={() => {
        if (root === null) return;
        if (uncertainFailure === null) commands.submit({ kind: "disable", root }, callbacks);
        else commands.retry(callbacks);
      }}
      onCancel={() => {
        commands.dismiss();
        onClose();
      }}
    >
      <p>停用后，扫描会跳过这个目录。</p>
      <p>已经入库的内容会保留，不会因为停用而被标为缺失。之后可以随时恢复。</p>
    </ConfirmDialog>
  );
}
