import { formatCount } from "../../../shared/lib/number";
import { ConfirmDialog, FailureNotice } from "../../../shared/ui";
import type { HistoryItemDTO } from "../api/decoders";
import { historyTitleOf } from "../format";
import type { RemoveTrackState } from "../hooks/use-remove-history-track";

type RemoveTrackDialogProps = {
  /** 要移除的曲目；null 时对话框关闭。 */
  item: HistoryItemDTO | null;
  state: RemoveTrackState;
  onConfirm: () => void;
  onCancel: () => void;
};

/**
 * 从历史中移除一首曲目前的确认：说明影响范围（只有自己的记录，这首曲目的全部播放次数）与后果（不能恢复）。
 * 失败时对话框保持打开，错误显示在其中，“确认”变成“重试移除”（移除是幂等的删除，重试安全）。
 */
export function RemoveTrackDialog({ item, state, onConfirm, onCancel }: RemoveTrackDialogProps) {
  const failed = state.status === "failed" ? state : null;
  return (
    <ConfirmDialog
      open={item !== null}
      title="从历史中移除这首曲目？"
      confirmLabel={failed === null ? "从历史中移除" : "重试移除"}
      pending={state.status === "pending"}
      error={failed === null ? undefined : <FailureNotice error={failed.error} context="write" title={failed.uncertain ? "结果未确认" : "移除失败"} />}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      {item === null ? null : (
        <>
          <p>
            将从你的播放历史中移除《{historyTitleOf(item)}》的全部 {formatCount(item.play_count)} 次记录，不影响其他成员。
          </p>
          <p>移除后不能恢复。</p>
        </>
      )}
    </ConfirmDialog>
  );
}
