import { useEffect, useRef, type ReactNode } from "react";
import type { Turn } from "../run/reduce";
import type { ThreadView } from "../hooks/use-run";
import { Answer } from "./answer";
import { RunEnded } from "./run-ended";
import { Steps } from "./steps";
import styles from "./thread.module.css";

/** 一个回合：用户的话（右侧浅底），过程，然后按顺序是各步的文字（中间步骤说的话与最终回答）。 */
function TurnView({ turn, live, waiting }: { turn: Turn; live: boolean; waiting: boolean }) {
  const texts = turn.texts.filter((text) => text.trim() !== "");
  const running = live && turn.items.some((item) => item.status === "running");
  return (
    <>
      {turn.userText === null ? null : (
        <div className={styles.ask}>
          <p>{turn.userText}</p>
        </div>
      )}
      <Steps items={turn.items} live={live} />
      {texts.map((text, index) => (
        <Answer key={index} text={text} cursor={live && !running && !waiting && index === texts.length - 1} />
      ))}
      {/* 刚开始、或这一步的工具都执行完了，在等模型的下一段输出。 */}
      {live && waiting ? <p className={styles.thinking}>正在想…</p> : null}
    </>
  );
}

/**
 * 对话内容：回合、最后一次运行的结束说明，以及调用方给的尾部（未配置引导、“正在别处回答”等）。
 * 新内容出现时，如果原本就停在底部，跟着滚到底部；用户往上翻看时不打扰。
 */
export function Thread({ view, busy, onContinue, empty, footer }: { view: ThreadView; busy: boolean; onContinue: () => void; empty?: ReactNode; footer?: ReactNode }) {
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const last = view.turns.at(-1);
  const contentKey = `${view.turns.length}:${last?.items.length ?? 0}:${last?.items.filter((item) => item.status !== "running").length ?? 0}:${last?.texts.join("").length ?? 0}:${view.end === null ? "" : view.end.kind}`;
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
          <TurnView key={turn.key} turn={turn} live={view.live && index === view.turns.length - 1} waiting={view.waiting} />
        ))}
        {view.end === null ? null : <RunEnded ended={view.end} hasSteps={(last?.items.length ?? 0) > 0} canContinue={view.canContinue} busy={busy} onContinue={onContinue} />}
        {footer}
      </div>
    </div>
  );
}
