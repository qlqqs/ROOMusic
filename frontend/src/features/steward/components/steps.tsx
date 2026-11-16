import { ChevronRight } from "lucide-react";
import { formatElapsed, summarizeSteps, type StepItem } from "../run/reduce";
import { toolArgumentOf, toolFailureOf, toolLabelOf, toolResultOf, toolWritesOf } from "../tool-labels";
import styles from "./thread.module.css";

/** 一次工具调用：图标、中文名称、主要参数（灰字），右侧是结果（数量或改动的说明）、失败原因、“进行中”或“等你确认”。参数来自曲库，只按文字显示。 */
function StepRow({ item }: { item: StepItem }) {
  const { label, icon: Icon } = toolLabelOf(item.name);
  const argument = toolArgumentOf(item.name, item.input, item.output);
  const result =
    item.status === "running"
      ? "进行中"
      : item.status === "awaiting_confirmation"
        ? "等你确认"
        : item.status === "failed"
          ? toolFailureOf(item.errorCode)
          : toolResultOf(item.name, item.input, item.output);
  return (
    <li className={styles.step} data-status={item.status}>
      <Icon size={14} aria-hidden="true" />
      <span className={styles.stepName}>
        {label}
        {argument === null ? null : <span className={styles.stepArg}>{argument}</span>}
      </span>
      {result === null ? <span /> : <span className={styles.stepResult}>{result}</span>}
    </li>
  );
}

/**
 * 过程：进行中逐条列出；结束后收成一行摘要，点开再看条目。只有只读工具时写“查了 N 处资料，用时 X”，
 * 执行过改动类工具（播放控制、收藏、歌单、历史的写入，曲库与艺人的治理）时写“做了 N 步，用时 X”；
 * 什么也没执行、只有等确认的修改时写“N 项修改等你确认”。没有工具调用时不显示。
 */
export function Steps({ items, live }: { items: readonly StepItem[]; live: boolean }) {
  if (items.length === 0) return null;
  const list = (
    <ul className={styles.stepList}>
      {items.map((item, index) => (
        <StepRow key={`${index}:${item.callId}`} item={item} />
      ))}
    </ul>
  );
  if (live) return <div className={styles.steps}>{list}</div>;
  const { count, durationMs } = summarizeSteps(items);
  const awaiting = items.filter((item) => item.status === "awaiting_confirmation").length;
  const done =
    count === 0 && awaiting > 0 ? `${awaiting} 项修改等你确认` : items.some((item) => item.executed && toolWritesOf(item.name)) ? `做了 ${count} 步` : `查了 ${count} 处资料`;
  return (
    <details className={styles.steps}>
      <summary className={styles.stepSummary}>
        <ChevronRight size={14} aria-hidden="true" className={styles.chevron} />
        {durationMs === null ? done : `${done}，用时 ${formatElapsed(durationMs)}`}
      </summary>
      {list}
    </details>
  );
}
