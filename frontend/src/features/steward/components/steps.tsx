import { ChevronRight } from "lucide-react";
import { formatElapsed, summarizeSteps, type StepItem } from "../run/reduce";
import { toolArgumentOf, toolCountOf, toolFailureOf, toolLabelOf } from "../tool-labels";
import styles from "./thread.module.css";

/** 一次工具调用：图标、中文名称、主要参数（灰字），右侧是数量、失败原因或“进行中”。参数来自曲库，只按文字显示。 */
function StepRow({ item }: { item: StepItem }) {
  const { label, icon: Icon } = toolLabelOf(item.name);
  const argument = toolArgumentOf(item.input, item.output);
  const result =
    item.status === "running" ? "进行中" : item.status === "failed" ? toolFailureOf(item.errorCode) : toolCountOf(item.name, item.input, item.output);
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
 * 过程：进行中逐条列出；结束后收成一行摘要“查了 N 处资料，用时 X”，点开再看条目。没有工具调用时不显示。
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
  return (
    <details className={styles.steps}>
      <summary className={styles.stepSummary}>
        <ChevronRight size={14} aria-hidden="true" className={styles.chevron} />
        {durationMs === null ? `查了 ${count} 处资料` : `查了 ${count} 处资料，用时 ${formatElapsed(durationMs)}`}
      </summary>
      {list}
    </details>
  );
}
