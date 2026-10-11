import { ConfirmDialog, FailureNotice } from "../../../shared/ui";
import type { ClearHistoryState } from "../hooks/use-clear-play-history";

type ClearHistoryDialogProps = {
  open: boolean;
  state: ClearHistoryState;
  onConfirm: () => void;
  onCancel: () => void;
};

/**
 * 清除播放历史前的确认：说明影响范围（只有自己的记录）与后果（不能恢复）。
 * 失败时对话框保持打开，错误显示在其中，“确认”变成“重试清除”（清除是幂等的，重试安全）。
 */
export function ClearHistoryDialog({ open, state, onConfirm, onCancel }: ClearHistoryDialogProps) {
  const failed = state.status === "failed" ? state : null;
  return (
    <ConfirmDialog
      open={open}
      title="清除播放历史？"
      confirmLabel={failed === null ? "清除播放历史" : "重试清除"}
      pending={state.status === "pending"}
      error={failed === null ? undefined : <FailureNotice error={failed.error} context="write" title={failed.uncertain ? "结果未确认" : "清除失败"} />}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      <p>将清除你自己的全部播放记录，不影响其他成员。</p>
      <p>清除后不能恢复。</p>
    </ConfirmDialog>
  );
}
