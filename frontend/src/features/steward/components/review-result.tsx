import { Ban, Check, Clock, Hand, type LucideIcon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { describeFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import { operationIdOf, type PlanDTO, type PlanItemDTO, type ReviewDecision, type ReviewItemDTO, type StewardMode } from "../api/decoders";
import { useRereviewPlan, useRevertPlanItem } from "../hooks/use-plans";
import type { PlanEntry, RevertState } from "../run/plan-store";
import type { StepItem } from "../run/reduce";
import { updateReview, type ReviewEntry, type ReviewView } from "../run/review-store";
import { toolFailureOf, toolLabelOf } from "../tool-labels";
import { ItemBody, PlanCard, RevertControl, seedOf } from "./plan-card";
import styles from "./review-result.module.css";

type ReviewResultProps = {
  conversationId: string;
  entry: PlanEntry;
  /** 会话详情里这份方案的完整内容（含审查已处理的项）；没有时用 entry.plan（运行里收到的只有等确认的项）。 */
  plan: PlanDTO;
  view: ReviewView;
  reviewState: ReviewEntry | undefined;
  /** 方案所在回合的过程条目：执行结果按调用标识从这里取。 */
  items: readonly StepItem[];
  baseSequence: number;
  busy: boolean;
  resumable: boolean;
  onContinue: () => void;
  name: string;
  /** 这个会话的对话模型：审查模型与它相同时说明独立性有限（C7 的 A8）。 */
  conversationModel: string;
  /** 会话的模式：只有 steward 模式能重新审查；确认卡片只在 assistant 模式有“不再问我”。 */
  mode: StewardMode;
};

type Tone = "done" | "ask" | "no" | "pending";
type RowStatus = { tone: Tone; label: string; detail: string | null; operationId: string | null };

const toneIcons: Readonly<Record<Tone, LucideIcon>> = { done: Check, ask: Hand, no: Ban, pending: Clock };

// 重新审查在开流前被拒绝。
const rereviewMessages: Readonly<Record<string, string>> = {
  plan_expired: "这份方案已经过期，不能再审查。",
  plan_not_found: "这份方案已经不在了。",
  conversation_busy: "这个对话正在回答，等它结束再重新审查。",
  review_unavailable: "审查仍然不可用，可以稍后再试，或者自己确认。",
  review_not_applicable: "当前模式不使用审查，可以自己确认。",
  steward_disabled: "管理员暂时关闭了成员使用管家。",
};

const riskLabels: Readonly<Record<ReviewItemDTO["risk"], string | null>> = { low: null, medium: "风险中等", high: "风险高" };

/**
 * 一项现在的状态（原型的“已执行／等你确认／没有执行”）：先看实际执行的结果（过程条目），再看审查的结论与方案的状态。
 * decision 为 null：审查不可用、用户自己确认的项。
 */
function rowStatusOf(entry: PlanEntry, item: PlanItemDTO, decision: ReviewDecision | null, step: StepItem | undefined, busy: boolean): RowStatus {
  const none = { detail: null, operationId: null };
  const open = entry.phase === "pending" || entry.phase === "submitting";
  if (step?.status === "succeeded") return { tone: "done", label: "已执行", detail: null, operationId: operationIdOf(step.output) ?? item.operation_id ?? null };
  if (step?.status === "failed") return { tone: "no", label: "没有执行", detail: toolFailureOf(step.errorCode), operationId: null };
  // 审查前为送审的调用发过 tool_started：被拒绝或转人工的项在拿到工具结果、方案事件之前仍是进行中，以审查结论为准。
  if (item.decision === "rejected_by_review" || decision === "rejected") return { tone: "no", label: "没有执行", detail: toolFailureOf("rejected_by_review"), operationId: null };
  if (decision === "needs_human_confirmation" && open) return { tone: "ask", label: "等你确认", ...none };
  if (step?.status === "running") return { tone: "pending", label: "正在执行", ...none };
  if (item.operation_id !== undefined && item.operation_id !== null) return { tone: "done", label: "已执行", detail: null, operationId: item.operation_id };
  switch (entry.phase) {
    case "superseded":
      return { tone: "no", label: "没有执行", detail: "方案已作废", operationId: null };
    case "expired":
      return { tone: "no", label: "没有执行", detail: "方案已过期", operationId: null };
    case "gone":
      return { tone: "no", label: "没有执行", detail: "方案已失效", operationId: null };
  }
  if (decision === "approved" && busy) return { tone: "pending", label: "正在执行", ...none };
  // 批准之后、执行之前方案或证据变了：没有执行，等重新审查（或用户自己确认）。
  if (decision === "approved" && open) return { tone: "no", label: "没有执行", detail: "批准之后内容变了，需要重新审查", operationId: null };
  if (open) return { tone: "ask", label: "等你确认", ...none };
  if (entry.accepted?.includes(item.id) === true) return { tone: "pending", label: busy ? "正在执行" : "还没执行", ...none };
  return { tone: "no", label: "没有执行", detail: toolFailureOf("declined_by_user"), operationId: null };
}

/** 一项的理由：审查模型给的原样按文字显示；策略规则转人工的说明是规则要求，不是审查模型的判断。 */
function Reasons({ review }: { review: ReviewItemDTO }) {
  const risk = riskLabels[review.risk];
  const reasons = review.reasons.join("；");
  return (
    <p className={styles.reason}>
      {review.source === "policy" ? <span className={styles.policy}>按规则交给你确认，不是审查模型的判断。</span> : null}
      {reasons === "" ? null : <span>{reasons}</span>}
      {risk === null ? null : <span className={styles.risk}>{risk}</span>}
    </p>
  );
}

type RowProps = {
  item: PlanItemDTO;
  review: ReviewItemDTO | null;
  status: RowStatus;
  revertState: RevertState | undefined;
  onRevert: (operationId: string, key: string | null) => void;
};

function Row({ item, review, status, revertState, onRevert }: RowProps) {
  const Icon = toneIcons[status.tone];
  const { operationId } = status;
  return (
    <li className={styles.row}>
      <span className={styles.status} data-tone={status.tone}>
        <Icon size={14} aria-hidden="true" />
        {status.label}
      </span>
      <div className={styles.main}>
        <ItemBody item={item} />
        {status.detail === null ? null : <p className={styles.detail}>{status.detail}</p>}
        {review === null ? null : <Reasons review={review} />}
      </div>
      <span className={styles.actions}>
        {status.tone === "done" && item.revertible && operationId !== null ? (
          <RevertControl label={`${toolLabelOf(item.tool).label} ${item.target}`} state={revertState} onRevert={(key) => onRevert(operationId, key)} />
        ) : null}
      </span>
    </li>
  );
}

/** 方案不能再处理时的说明（与修改方案卡片相同的情形）。 */
function closedNote(phase: PlanEntry["phase"], name: string): string | null {
  switch (phase) {
    case "superseded":
      return "你发了新消息，这份方案没有执行。";
    case "expired":
      return `这份方案已经过期，不能应用。需要的话请${name}重新整理一份。`;
    case "gone":
      return "这份方案已经在别处处理或失效，不能再应用。";
    default:
      return null;
  }
}

/**
 * 审查结果（C7，原型的 section.review）：标题写明由哪个模型独立审查、不带这次对话的内容；与对话是同一个模型时说明独立性有限。
 * 每项是状态（已执行、等你确认、没有执行）、修改摘要与理由，已执行且可撤销的项有“撤销”；转给用户确认的项在下面的确认卡片里逐项决定
 * （沿用修改方案卡片：不可撤销的修改没有“不再问我”）。审查中只列出送审的项；审查不可用时整份方案没有执行，可以重新审查或自己确认。
 * 理由来自审查模型，与目标名、旧值新值一样只按文本节点渲染，不走 Markdown。
 */
export function ReviewResult({ conversationId, entry, plan, view, reviewState, items, baseSequence, busy, resumable, onContinue, name, conversationModel, mode }: ReviewResultProps) {
  const headingId = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const revert = useRevertPlanItem(conversationId);
  const rereview = useRereviewPlan(conversationId);
  // 只有用户刚点了“我自己确认”时，焦点才移到展开的卡片；之后重新挂载（切换会话再回来）不抢焦点。
  const [focusConfirm, setFocusConfirm] = useState(false);
  const { phase } = entry;
  const open = phase === "pending" || phase === "submitting";
  const model = view.kind === "reviewing" ? view.model : view.review.model;
  const review = view.kind === "review" ? view.review : null;
  const unavailable = review?.status === "unavailable";
  const decisions = new Map((review?.status === "completed" ? review.items : []).map((item) => [item.item_id, item]));
  const stepOf = (item: PlanItemDTO) => items.find((step) => step.callId === item.call_id);

  // 用户在嵌入的确认卡片里提交后卡片收起：焦点移到审查结果的标题，读屏从结果开始读。
  const previous = useRef(phase);
  useEffect(() => {
    if (previous.current === "submitting" && phase === "decided") heading.current?.focus();
    previous.current = phase;
  }, [phase]);

  // 需要重新审查：审查不可用，或批准的项在执行前发现内容变了、没有执行（运行以 review_unavailable 结束）。
  const undecided = (item: PlanItemDTO) => item.decision === undefined || item.decision === "pending";
  const stale = !busy && review?.status === "completed" && plan.items.some((item) => undecided(item) && decisions.get(item.id)?.decision === "approved");
  const needsReview = unavailable || stale;
  const manual = reviewState?.manual === true;
  // 等用户决定的项：需要重新审查而用户选了自己确认时是全部还没处理的项，否则是审查转给用户的项。
  const confirmable = plan.items.filter((item) => undecided(item) && (needsReview ? manual : decisions.get(item.id)?.decision === "needs_human_confirmation"));
  const showConfirm = open && view.kind === "review" && confirmable.length > 0;
  const confirmEntry: PlanEntry = { ...entry, plan: { ...entry.plan, items: confirmable } };

  // 审查完成时按审查的项列出（方案里有、审查里也有的）；审查中与不可用时列出方案的全部项。
  const rows = review?.status === "completed" ? plan.items.filter((item) => decisions.has(item.id)) : plan.items;
  const note = closedNote(phase, name);
  const failure = reviewState?.failure ?? null;
  const refusal = failure === null ? null : describeFailure(failure, { context: "write", messages: rereviewMessages });

  const onRereview = async () => {
    if (await rereview(entry, seedOf(entry, items), baseSequence)) heading.current?.focus();
  };

  return (
    <section className={styles.review} aria-labelledby={headingId}>
      <header className={styles.head}>
        <h3 id={headingId} ref={heading} tabIndex={-1}>
          审查结果
        </h3>
        <span className={styles.meta}>
          <span>由 {model} 独立审查，不带这次对话的内容</span>
          {model === conversationModel ? <span className={styles.same}>审查与对话用的是同一个模型，独立性有限；可以在设置里另选审查模型。</span> : null}
        </span>
      </header>
      {view.kind === "reviewing" ? (
        <p className={styles.state} role="status">
          审查中…
        </p>
      ) : unavailable ? (
        <p className={styles.state} role="status">
          审查不可用，这份方案什么都没有执行。
        </p>
      ) : null}
      <ul className={styles.rows}>
        {rows.map((item) => {
          const decision = decisions.get(item.id) ?? null;
          const status: RowStatus =
            view.kind === "reviewing" ? { tone: "pending", label: "审查中", detail: null, operationId: null } : rowStatusOf(entry, item, decision?.decision ?? null, stepOf(item), busy);
          return (
            <Row
              key={item.id}
              item={item}
              review={decision}
              status={status}
              revertState={entry.reverts.get(item.id)}
              onRevert={(operationId, key) => void revert(entry.plan, item.id, operationId, key)}
            />
          );
        })}
      </ul>
      {needsReview && open && !manual ? (
        <footer className={styles.foot}>
          {failure === null || refusal === null ? null : (
            <Notice tone={failure.kind === "http" && failure.status === 409 ? "warning" : "error"} requestId={refusal.requestId}>
              {refusal.message}
            </Notice>
          )}
          <p className={styles.note}>
            {stale ? "批准之后内容有变化，批准的项没有执行。" : ""}
            {mode === "steward" ? "可以重新审查，或者自己逐项确认。" : "可以自己逐项确认。"}
          </p>
          <div className={styles.buttons}>
            {mode === "steward" ? (
              <Button variant="primary" busy={reviewState?.submitting === true} disabled={busy} onClick={() => void onRereview()}>
                {reviewState?.submitting === true ? "正在提交…" : "重新审查"}
              </Button>
            ) : null}
            <Button
              disabled={busy || reviewState?.submitting === true}
              onClick={() => {
                setFocusConfirm(true);
                updateReview(conversationId, entry.plan.plan_id, () => ({ manual: true, failure: null }));
              }}
            >
              我自己确认
            </Button>
          </div>
        </footer>
      ) : null}
      {showConfirm ? (
        <div className={styles.confirm}>
          <PlanCard
            conversationId={conversationId}
            entry={confirmEntry}
            items={items}
            baseSequence={baseSequence}
            busy={busy}
            resumable={false}
            onContinue={onContinue}
            name={name}
            title={needsReview ? "修改方案" : "等你确认的修改"}
            focusOnMount={focusConfirm}
            autoApprove={mode === "assistant"}
          />
        </div>
      ) : null}
      {phase === "decided" && resumable ? (
        <footer className={styles.foot}>
          <p className={styles.note}>回答停在了这里，有确认过的修改还没执行。点“继续”接着执行；发新消息不会执行它们，会记为未执行。</p>
          <div className={styles.buttons}>
            <Button variant="primary" onClick={onContinue}>
              继续
            </Button>
          </div>
        </footer>
      ) : null}
      {note === null ? null : (
        <footer className={styles.foot}>
          <p className={styles.note} role="status">
            {note}
          </p>
        </footer>
      )}
    </section>
  );
}
