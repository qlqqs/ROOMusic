import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, ConfirmDialog, FailureNotice, Notice } from "../../../shared/ui";
import type { RevertScope } from "../api/endpoints";
import { skippedSummary } from "../curation-text";
import type { ScopeRevertState } from "../hooks/use-scope-revert";
import styles from "./curation-sections.module.css";

/** 要撤销的范围与它的说明：按批给出开始时间与还能撤的条数；按来源给出源名、条数（读不到时为 null）与源是否还开着。 */
export type ScopeRevertTarget =
  | { scope: RevertScope & { kind: "batch" }; openedAt: string; remaining: number; ongoing: boolean }
  | { scope: RevertScope & { kind: "provider" }; name: string; total: number | null; sourceEnabled: boolean };

/**
 * 按批或按来源撤销前的确认。撤销分段进行（每段至多 100 条，同一个幂等键），进行中不能关闭，礼貌播报已撤销的条数；
 * 网络中断时停在原处，“继续撤销”用同一个键从中断的地方接着做。结果在对话框外就近显示（见 ScopeRevertResult）。
 */
export function ScopeRevertDialog({
  target,
  state,
  onConfirm,
  onResume,
  onCancel,
}: {
  target: ScopeRevertTarget | null;
  state: ScopeRevertState;
  onConfirm: () => void;
  onResume: () => void;
  onCancel: () => void;
}) {
  const running = state.phase === "running";
  const interrupted = state.phase === "interrupted";
  const open = target !== null && (state.phase === "idle" || running || interrupted);
  return (
    <ConfirmDialog
      open={open}
      title={target !== null && "name" in target ? `撤销来自 ${target.name} 的全部自动修改？` : "撤销这一批？"}
      confirmLabel={interrupted ? "继续撤销" : "撤销"}
      pending={running}
      error={interrupted ? <FailureNotice error={state.error} context="write" title={`已撤销 ${formatCount(state.reverted)} 条，还没有做完`} /> : undefined}
      onConfirm={interrupted ? onResume : onConfirm}
      onCancel={onCancel}
    >
      {target === null ? null : <ScopeDescription target={target} />}
      <p className={styles.groupNote}>撤销之后，同一个值不会再自动写入；需要时可以在“不再提出”里清除。</p>
      {running ? (
        <p role="status" aria-live="polite">
          已撤销 {formatCount(state.reverted)} 条…
        </p>
      ) : null}
    </ConfirmDialog>
  );
}

function ScopeDescription({ target }: { target: ScopeRevertTarget }) {
  if (target.scope.kind === "batch" && "openedAt" in target) {
    return (
      <>
        <p>
          撤销 {formatDateTime(target.openedAt)} 开始的这一批里还能撤销的 {formatCount(target.remaining)} 条自动修改，按时间从后往前逐条撤销。
        </p>
        {target.ongoing ? <p className={styles.groupNote}>这一批还在进行中：撤销开始之后新写入的不包括在内。</p> : null}
      </>
    );
  }
  if ("name" in target) {
    return (
      <>
        <p>
          撤销来自 {target.name} 的{target.total === null ? "全部" : ` ${formatCount(target.total)} 条`}自动修改（已经撤销过的跳过），按时间从后往前逐条撤销。
        </p>
        {target.sourceEnabled ? <p className={styles.groupNote}>这个源还开着，后台还会继续用它写入新内容；可以先关闭这个源。</p> : null}
      </>
    );
  }
  return null;
}

/** 撤销完成后的结果：撤了多少、跳过了多少与原因；按批时写这一批的权威总数。 */
export function ScopeRevertResult({ state, onDismiss }: { state: ScopeRevertState; onDismiss: () => void }) {
  if (state.phase !== "done") return null;
  const reasons = skippedSummary(state.skipped);
  const head = state.batch === null
    ? `${state.atLeast ? "至少" : ""}撤销了 ${formatCount(state.reverted)} 条`
    : `这一批共 ${formatCount(state.batch.operations)} 条，已撤销 ${formatCount(state.batch.reverted)} 条`;
  return (
    <Notice tone={reasons.length === 0 ? "success" : "info"} action={<Button onClick={onDismiss}>知道了</Button>}>
      {head}
      {state.skipped.length === 0 ? "。" : `，跳过 ${formatCount(state.skipped.length)} 条：`}
      {reasons.length === 0 ? null : (
        <ul className={styles.resultList}>
          {reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      )}
    </Notice>
  );
}
