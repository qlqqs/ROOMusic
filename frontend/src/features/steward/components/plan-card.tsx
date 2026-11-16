import { useEffect, useId, useRef } from "react";
import { describeFailure, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { Button, Notice, VisuallyHidden } from "../../../shared/ui";
import { operationIdOf, type PlanItemDTO } from "../api/decoders";
import { useDecidePlan, useRevertPlanItem } from "../hooks/use-plans";
import { basisSourceLabel, planFieldLabel, planValueText } from "../plan-text";
import { autoApprovableTools, updatePlan, type PlanEntry, type RevertState } from "../run/plan-store";
import type { StepItem } from "../run/reduce";
import { toolFailureOf, toolLabelOf } from "../tool-labels";
import styles from "./plan-card.module.css";

type PlanCardProps = {
  conversationId: string;
  entry: PlanEntry;
  /** 方案所在回合的过程条目：执行结果按调用标识从这里取。 */
  items: readonly StepItem[];
  /** 已落库的最后一条消息的序号（确认之后的运行从这里接着显示）。 */
  baseSequence: number;
  /** 这个会话正在回答：不能提交；已接受、还没有结果的项是“正在执行”。 */
  busy: boolean;
  /** 已确认、有接受的项还没执行完（运行被停止或服务重启后）：卡片给出“继续”。 */
  resumable: boolean;
  onContinue: () => void;
  /** 管家的名字。 */
  name: string;
  /** 卡片标题；审查结果里转给用户确认的部分用“等你确认的修改”（C7）。 */
  title?: string;
  /** 出现时把焦点移到标题（审查不可用后用户选了“我自己确认”）。 */
  focusOnMount?: boolean;
  /** 显示“这次对话里不再问我”：只有 assistant 模式；steward 模式的确认不接受免确认工具（C7）。 */
  autoApprove?: boolean;
};

// 确认请求在开流前被拒绝（设计“方案与挂起”第 4 步的校验顺序）。
const decisionMessages: Readonly<Record<string, string>> = {
  plan_digest_mismatch: "方案的内容变了，已换成最新的内容，请看过再确认。",
  plan_expired: "这份方案已经过期，不能应用。",
  plan_not_found: "这份方案已经不在了。",
  conversation_busy: "这个对话正在回答，等它结束再确认。",
  steward_disabled: "管理员暂时关闭了成员使用管家。",
};

export const revertMessages: Readonly<Record<string, string>> = {
  revert_conflict: "之后又有修改，不能撤销这一项。",
  already_reverted: "这一项已经撤销过了。",
  not_revertible: "这一项不能撤销。",
};

/** 头部右侧的一句话：几项、能不能撤销，或方案现在的状态。 */
function metaOf(entry: PlanEntry): string {
  const { items } = entry.plan;
  switch (entry.phase) {
    case "pending":
    case "submitting": {
      const fixed = items.filter((item) => !item.revertible).length;
      if (fixed === 0) return `${items.length} 项，都可以撤销`;
      if (fixed === items.length) return `${items.length} 项，都不能撤销`;
      return `${items.length} 项，其中 ${fixed} 项不能撤销`;
    }
    case "decided":
      return entry.accepted === null || entry.accepted.length === 0 ? "没有修改" : `应用了 ${entry.accepted.length} 项`;
    case "superseded":
      return "已作废";
    case "expired":
      return "已过期";
    case "gone":
      return "已失效";
  }
}

type Outcome = { tone: "done" | "pending" | "skipped" | "failed"; text: string; operationId: string | null };

/**
 * 一项的结果：作废的是未执行；已决定的看过程里对应条目的状态。还没有结果时：没接受的是“未选”，接受了的在回答中是“正在执行”，
 * 不在回答中（停止或服务重启之后）是“还没执行”。
 */
function outcomeOf(entry: PlanEntry, item: PlanItemDTO, step: StepItem | undefined, busy: boolean): Outcome {
  if (entry.phase === "superseded") return { tone: "skipped", text: "未执行", operationId: null };
  const accepted = entry.accepted?.includes(item.id) === true;
  switch (step?.status) {
    case "succeeded":
      return { tone: "done", text: "已执行", operationId: operationIdOf(step.output) ?? item.operation_id ?? null };
    case "failed":
      if (step.errorCode === "declined_by_user") return { tone: "skipped", text: "未选", operationId: null };
      if (step.errorCode === "not_executed") return { tone: "skipped", text: "未执行", operationId: null };
      return { tone: "failed", text: `没有执行：${toolFailureOf(step.errorCode)}`, operationId: null };
    case "running":
      return { tone: "pending", text: "正在执行", operationId: null };
    default:
      if (!accepted) return { tone: "skipped", text: "未选", operationId: null };
      return { tone: "pending", text: busy ? "正在执行" : "还没执行", operationId: null };
  }
}

/** 字段的旧值（删除线）与新值；读屏听到“原来”“改为”。值来自曲库，只按文本节点渲染。 */
function Changes({ item }: { item: PlanItemDTO }) {
  if (item.changes.length === 0) return null;
  return (
    <dl className={styles.changes}>
      {item.changes.map((change, index) => (
        <div key={index} className={styles.change}>
          <dt>{planFieldLabel(change.field)}</dt>
          <dd>
            <del>
              <VisuallyHidden>原来</VisuallyHidden>
              <span>{planValueText(change.before)}</span>
            </del>
            <ins>
              <VisuallyHidden>改为</VisuallyHidden>
              <span>{planValueText(change.after)}</span>
            </ins>
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function ItemBody({ item, labelFor }: { item: PlanItemDTO; labelFor?: string }) {
  const { label } = toolLabelOf(item.tool);
  const what = (
    <>
      <span className={styles.tool}>{label}</span> <span className={styles.target}>{item.target}</span>
    </>
  );
  return (
    <div className={styles.body}>
      <div className={styles.what}>
        {labelFor === undefined ? <span>{what}</span> : <label htmlFor={labelFor}>{what}</label>}
        {item.revertible ? null : <span className={styles.irreversible}>不可撤销</span>}
      </div>
      <Changes item={item} />
      {item.basis === undefined || item.basis === null ? null : (
        <p className={styles.why}>
          依据（{basisSourceLabel(item.basis.source)}）：{item.basis.note}
        </p>
      )}
    </div>
  );
}

function PendingItem({ conversationId, entry, item, disabled }: { conversationId: string; entry: PlanEntry; item: PlanItemDTO; disabled: boolean }) {
  const id = useId();
  return (
    <li className={styles.item}>
      <input
        id={id}
        type="checkbox"
        checked={entry.selected.has(item.id)}
        disabled={disabled}
        onChange={(event) => {
          const checked = event.target.checked;
          updatePlan(conversationId, entry.plan, (current) => {
            const selected = new Set(current.selected);
            if (checked) selected.add(item.id);
            else selected.delete(item.id);
            return { selected };
          });
        }}
      />
      <ItemBody item={item} labelFor={id} />
    </li>
  );
}

export function RevertControl({ label, state, onRevert }: { label: string; state: RevertState | undefined; onRevert: (key: string | null) => void }) {
  if (state?.status === "done") return <span>已撤销</span>;
  if (state?.status === "pending") {
    return (
      <Button busy>
        正在撤销…<VisuallyHidden>（{label}）</VisuallyHidden>
      </Button>
    );
  }
  if (state?.status === "failed") {
    const uncertain = isOutcomeUncertain(state.failure);
    return (
      <>
        <span role="alert">{describeFailure(state.failure, { context: "write", messages: revertMessages }).message}</span>
        {uncertain ? (
          <Button onClick={() => onRevert(state.key)}>
            重试撤销<VisuallyHidden>（{label}）</VisuallyHidden>
          </Button>
        ) : null}
      </>
    );
  }
  return (
    <Button onClick={() => onRevert(null)}>
      撤销<VisuallyHidden>（{label}）</VisuallyHidden>
    </Button>
  );
}

type ResultItemProps = { entry: PlanEntry; item: PlanItemDTO; step: StepItem | undefined; busy: boolean; onRevert: (operationId: string, key: string | null) => void };

function ResultItem({ entry, item, step, busy, onRevert }: ResultItemProps) {
  const showOutcome = entry.phase === "decided" || entry.phase === "superseded";
  const outcome = showOutcome ? outcomeOf(entry, item, step, busy) : null;
  const operationId = outcome?.operationId ?? null;
  return (
    <li className={styles.item} data-static="">
      <div className={styles.body}>
        <ItemBody item={item} />
        {outcome === null ? null : (
          <div className={styles.outcome} data-tone={outcome.tone}>
            <span>{outcome.text}</span>
            {item.revertible && operationId !== null ? (
              <RevertControl label={`${toolLabelOf(item.tool).label} ${item.target}`} state={entry.reverts.get(item.id)} onRevert={(key) => onRevert(operationId, key)} />
            ) : null}
          </div>
        )}
      </div>
    </li>
  );
}

/** 确认请求在开流前被拒绝的说明：摘要不一致、对话正忙是提醒，其余是错误。 */
function DecisionFailure({ failure }: { failure: ApiFailure }) {
  const { message, requestId } = describeFailure(failure, { context: "write", messages: decisionMessages });
  return (
    <Notice tone={failure.kind === "http" && failure.status === 409 ? "warning" : "error"} requestId={requestId}>
      {message}
    </Notice>
  );
}

/** 方案项对应的过程条目（等待确认的），作为确认或重新审查的运行的初始条目；过程里没有时按方案项补上。 */
export function seedOf(entry: PlanEntry, items: readonly StepItem[]): StepItem[] {
  return entry.plan.items.map(
    (item) =>
      items.find((step) => step.callId === item.call_id) ?? {
        callId: item.call_id,
        name: item.tool,
        input: null,
        status: "awaiting_confirmation",
        output: null,
        errorCode: null,
        durationMs: null,
        executed: false,
        textsBefore: 0,
      },
  );
}

/**
 * 修改方案（C6）：等待决定时逐项勾选（默认全选），“应用 N 项修改”（没有勾选时停用）或“不修改”；方案里全部可以撤销的工具各有一个
 * “这次对话里不再问我”，不可撤销的修改每次都要确认。决定之后变成结果列表（已执行、失败原因、未选、未执行），已执行且可撤销的项可以撤销；
 * 有接受的项还没执行完（停止或服务重启之后）时给出“继续”，发新消息不会执行它们。作废、过期与已失效的方案只列出内容，不能再应用。
 * 目标名、旧值新值与依据都来自曲库或模型，只按文本节点渲染。
 */
export function PlanCard({ conversationId, entry, items, baseSequence, busy, resumable, onContinue, name, title = "修改方案", focusOnMount = false, autoApprove = true }: PlanCardProps) {
  const headingId = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const decide = useDecidePlan(conversationId);
  const revert = useRevertPlanItem(conversationId);
  const { plan, phase } = entry;
  const editable = phase === "pending" || phase === "submitting";
  const submitting = phase === "submitting";
  const count = plan.items.filter((item) => entry.selected.has(item.id)).length;
  const tools = autoApprove ? autoApprovableTools(plan) : [];

  // 提交后按钮消失：焦点移到卡片标题，读屏从结果开始读。
  useEffect(() => {
    if (focusOnMount) heading.current?.focus();
  }, [focusOnMount]);
  const previous = useRef(phase);
  useEffect(() => {
    if (previous.current === "submitting" && phase === "decided") heading.current?.focus();
    previous.current = phase;
  }, [phase]);

  const submit = (accepted: readonly string[]) => {
    if (busy || submitting) return;
    void decide(entry, accepted, seedOf(entry, items), baseSequence, autoApprove);
  };

  return (
    <section className={styles.plan} aria-labelledby={headingId}>
      <header className={styles.head}>
        <h3 id={headingId} ref={heading} tabIndex={-1}>
          {title}
        </h3>
        <span className={styles.meta}>{metaOf(entry)}</span>
      </header>
      <ul className={styles.items}>
        {plan.items.map((item) =>
          editable ? (
            <PendingItem key={item.id} conversationId={conversationId} entry={entry} item={item} disabled={submitting} />
          ) : (
            <ResultItem
              key={item.id}
              entry={entry}
              item={item}
              step={items.find((step) => step.callId === item.call_id)}
              busy={busy}
              onRevert={(operationId, key) => void revert(plan, item.id, operationId, key)}
            />
          ),
        )}
      </ul>
      {editable ? (
        <footer className={styles.foot}>
          {entry.failure === null ? null : (
            <DecisionFailure failure={entry.failure} />
          )}
          <div className={styles.row}>
            <Button
              variant="primary"
              busy={submitting}
              disabled={count === 0 || busy}
              onClick={() => submit(plan.items.filter((item) => entry.selected.has(item.id)).map((item) => item.id))}
            >
              {submitting ? "正在提交…" : `应用 ${count} 项修改`}
            </Button>
            <Button disabled={busy || submitting} onClick={() => submit([])}>
              不修改
            </Button>
          </div>
          {tools.map((tool) => (
            <label key={tool} className={styles.auto}>
              <input
                type="checkbox"
                checked={entry.autoApprove.has(tool)}
                disabled={submitting}
                onChange={(event) => {
                  const checked = event.target.checked;
                  updatePlan(conversationId, plan, (current) => {
                    const autoApprove = new Set(current.autoApprove);
                    if (checked) autoApprove.add(tool);
                    else autoApprove.delete(tool);
                    return { autoApprove };
                  });
                }}
              />
              这次对话里，{toolLabelOf(tool).label}不再问我
            </label>
          ))}
        </footer>
      ) : phase === "decided" ? (
        resumable ? (
          <footer className={styles.foot}>
            <p className={styles.note}>回答停在了这里，有确认过的修改还没执行。点“继续”接着执行；发新消息不会执行它们，会记为未执行。</p>
            <div className={styles.row}>
              <Button variant="primary" onClick={onContinue}>
                继续
              </Button>
            </div>
          </footer>
        ) : null
      ) : (
        <footer className={styles.foot}>
          <p className={styles.note} role="status">
            {phase === "superseded"
              ? "你发了新消息，这份方案没有执行。"
              : phase === "expired"
                ? `这份方案已经过期，不能应用。需要的话请${name}重新整理一份。`
                : "这份方案已经在别处处理或失效，不能再应用。"}
          </p>
        </footer>
      )}
    </section>
  );
}
