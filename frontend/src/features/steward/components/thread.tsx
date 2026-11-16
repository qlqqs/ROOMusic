import { useEffect, useRef, type ReactNode } from "react";
import type { Turn } from "../run/reduce";
import type { PlacedPlan } from "../hooks/use-plans";
import type { ThreadView } from "../hooks/use-run";
import { Answer } from "./answer";
import { RunEnded } from "./run-ended";
import { Citations } from "./sources";
import { Steps } from "./steps";
import styles from "./thread.module.css";

const nonEmpty = (text: string) => text.trim() !== "";

/**
 * 一个回合：用户的话（右侧浅底），过程，然后按顺序是各步的文字（中间步骤说的话与最终回答）。
 * 有修改方案时，方案卡片排在提出它的那一步的文字之后，确认之后的回答接在卡片下面。回答引用的来源（C8）列在这一回合的回答下面。
 */
function TurnView({ turn, live, waiting, plans, renderPlan }: { turn: Turn; live: boolean; waiting: boolean; plans: readonly PlacedPlan[]; renderPlan: (plan: PlacedPlan, turn: Turn) => ReactNode }) {
  const cut = plans.length === 0 ? turn.texts.length : Math.min(...plans.map((plan) => plan.textsBefore));
  const before = turn.texts.slice(0, cut).filter(nonEmpty);
  const after = turn.texts.slice(cut).filter(nonEmpty);
  const running = live && turn.items.some((item) => item.status === "running");
  const cursorAt = live && !running && !waiting ? before.length + after.length - 1 : -1;
  return (
    <>
      {turn.userText === null ? null : (
        <div className={styles.ask}>
          <p>{turn.userText}</p>
        </div>
      )}
      <Steps items={turn.items} live={live} />
      {before.map((text, index) => (
        <Answer key={index} text={text} cursor={index === cursorAt} />
      ))}
      {plans.map((plan) => renderPlan(plan, turn))}
      {after.map((text, index) => (
        <Answer key={before.length + index} text={text} cursor={before.length + index === cursorAt} />
      ))}
      <Citations citations={turn.citations} />
      {/* 刚开始、或这一步的工具都执行完了，在等模型的下一段输出。 */}
      {live && waiting ? <p className={styles.thinking}>正在想…</p> : null}
    </>
  );
}

type ThreadProps = {
  view: ThreadView;
  busy: boolean;
  onContinue: () => void;
  empty?: ReactNode;
  footer?: ReactNode;
  /** 修改方案（C6）与它们所在的回合；卡片由调用方渲染。 */
  plans?: readonly PlacedPlan[];
  renderPlan?: (plan: PlacedPlan, turn: Turn) => ReactNode;
};

const noPlans: readonly PlacedPlan[] = [];

/**
 * 对话内容：回合、最后一次运行的结束说明，以及调用方给的尾部（未配置引导、“正在别处回答”等）。
 * 新内容出现时，如果原本就停在底部，跟着滚到底部；用户往上翻看时不打扰。
 */
export function Thread({ view, busy, onContinue, empty, footer, plans = noPlans, renderPlan = () => null }: ThreadProps) {
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const last = view.turns.at(-1);
  const contentKey = `${view.turns.length}:${last?.items.length ?? 0}:${last?.items.filter((item) => item.status !== "running").length ?? 0}:${last?.texts.join("").length ?? 0}:${last?.citations.length ?? 0}:${view.end === null ? "" : view.end.kind}:${plans.map((plan) => `${plan.entry.plan.plan_id}:${plan.entry.phase}`).join(",")}`;
  useEffect(() => {
    const element = scroller.current;
    if (element !== null && pinned.current) element.scrollTop = element.scrollHeight;
  }, [contentKey]);
  return (
    <div
      ref={scroller}
      className={styles.thread}
      onScroll={(event) => {
        const element = event.currentTarget;
        pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight < 48;
      }}
    >
      <div className={styles.threadBody}>
        {view.turns.length === 0 && view.end === null ? empty : null}
        {view.turns.map((turn, index) => (
          <TurnView
            key={turn.key}
            turn={turn}
            live={view.live && index === view.turns.length - 1}
            waiting={view.waiting}
            plans={plans.filter((plan) => plan.turnKey === turn.key)}
            renderPlan={renderPlan}
          />
        ))}
        {view.end === null ? null : <RunEnded ended={view.end} hasSteps={(last?.items.length ?? 0) > 0} canContinue={view.canContinue && view.unfinishedPlans.size === 0} busy={busy} onContinue={onContinue} />}
        {footer}
      </div>
    </div>
  );
}
