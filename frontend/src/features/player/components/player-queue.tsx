import { useRef, useState } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { Button, ConfirmDialog } from "../../../shared/ui";
import { usePlayer } from "../player-provider";
import { failureLabels, statusLabels } from "./player-labels";
import styles from "./player-queue.module.css";

export function PlayerQueue() {
  const { engine, snapshot } = usePlayer();
  const [confirm, setConfirm] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const list = useRef<HTMLOListElement>(null);
  function remove(entryId: string, index: number) {
    engine.removeEntry(entryId);
    // 删除后的焦点落在相邻条目的同类按钮；列表空了则回到区段标题。
    queueMicrotask(() => {
      const rows = list.current?.querySelectorAll<HTMLLIElement>("li");
      const target = rows?.[Math.min(index, (rows?.length ?? 0) - 1)];
      (target?.querySelector<HTMLButtonElement>("[data-remove]") ?? heading.current ?? document.getElementById("main"))?.focus();
    });
  }
  function move(entryId: string, delta: -1 | 1) {
    const at = snapshot.queue.entries.findIndex((entry) => entry.entryId === entryId);
    engine.moveEntry(entryId, delta);
    const reachesEdge = delta === -1 ? at === 1 : at === snapshot.queue.entries.length - 2;
    if (!reachesEdge) return;
    // 移到边界后方向按钮会禁用，转到同一条目的播放按钮，不把焦点丢到 body。
    queueMicrotask(() => {
      const row = Array.from(list.current?.children ?? []).find((child) => child instanceof HTMLElement && child.dataset.entryId === entryId);
      row?.querySelector<HTMLButtonElement>("button")?.focus();
    });
  }
  return <section id="queue" aria-labelledby="queue-heading" className={styles.section}>
    <div className={styles.header}><h2 ref={heading} id="queue-heading" tabIndex={-1}>队列（{snapshot.queue.entries.length}）</h2><Button disabled={snapshot.queue.entries.length === 0} onClick={() => setConfirm(true)}>清空队列</Button></div>
    {snapshot.queue.mode === "shuffle" ? <p>随机模式：列表顺序不代表实际播放顺序，上一首沿播放历史返回。</p> : null}
    <ol ref={list} role="list" className={styles.list}>
      {snapshot.queue.entries.map((entry, index) => {
        const current = entry.entryId === snapshot.current?.entryId;
        const failure = snapshot.failures[entry.entryId];
        return <li key={entry.entryId} data-entry-id={entry.entryId} className={styles.row} aria-current={current ? "true" : undefined}>
          <div className={styles.description}><p className={styles.title} title={entry.track.title}><span className={styles.number}>{index + 1}.</span> {entry.track.title}</p><p className={`${styles.secondary} ${styles.metadata}`} title={`${entry.track.artist} — ${entry.track.albumTitle}`}>{entry.track.artist} — {entry.track.albumTitle}</p>
            {current ? <p className={styles.secondary}>当前项 · {statusLabels[snapshot.status]}</p> : null}
            {failure === undefined ? null : <p>{failureLabels[failure]}</p>}
          </div>
          <span className={styles.duration}>{formatDuration(entry.track.durationSeconds)}</span>
          <div className={styles.actions} role="group" aria-label={`第 ${index + 1} 项操作：${entry.track.title}`}>
            <Button onClick={() => engine.playEntry(entry.entryId)}>播放此项</Button>
            <Button disabled={index === 0} onClick={() => move(entry.entryId, -1)}>上移</Button>
            <Button disabled={index === snapshot.queue.entries.length - 1} onClick={() => move(entry.entryId, 1)}>下移</Button>
            <Button data-remove onClick={() => remove(entry.entryId, index)}>移除</Button>
          </div>
        </li>;
      })}
    </ol>
    <ConfirmDialog open={confirm} title="清空播放队列？" confirmLabel="清空队列" onCancel={() => setConfirm(false)} onConfirm={() => { engine.clearQueue(); setConfirm(false); queueMicrotask(() => (heading.current ?? document.getElementById("main"))?.focus()); }}><p>播放将停止，队列中的全部条目会被移除。</p></ConfirmDialog>
  </section>;
}
