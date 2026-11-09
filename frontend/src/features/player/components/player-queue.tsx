import { ArrowDown, ArrowUp, Play, Trash2, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { Button, ConfirmDialog } from "../../../shared/ui";
import type { PlayerTrack } from "../engine/queue";
import { usePlayer } from "../player-provider";
import { failureLabels, statusLabels } from "./player-labels";
import styles from "./player-queue.module.css";

type PlayerQueueProps = {
  /** 标题的 id：队列抽屉用它作对话框的名称。 */
  headingId: string;
  /** 关闭抽屉。 */
  onClose: () => void;
  /** 队列标题旁的动作（例如“存为歌单”）：拿到的是此刻队列里的曲目（按队列顺序，可能重复）；由 app 组合，player 不认识歌单。 */
  renderQueueActions?: ((tracks: readonly PlayerTrack[]) => ReactNode) | undefined;
};

/** 队列抽屉的内容：标题、存为歌单与清空，条目可播放、上移、下移、移除。 */
export function PlayerQueue({ headingId, onClose, renderQueueActions }: PlayerQueueProps) {
  const { engine, snapshot } = usePlayer();
  const [confirm, setConfirm] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const list = useRef<HTMLOListElement>(null);
  // 打开时把当前项滚到可视区。
  useEffect(() => {
    list.current?.querySelector("[aria-current]")?.scrollIntoView({ block: "center" });
  }, []);
  // 删除后的焦点落在相邻条目的同类按钮；列表空了则回到抽屉标题。移除要等服务端的响应，所以等这一项真的从列表里消失后再移焦点。
  const removing = useRef<{ entryId: string; index: number } | null>(null);
  const entries = snapshot.queue.entries;
  useEffect(() => {
    const pending = removing.current;
    if (pending === null || entries.some((entry) => entry.entryId === pending.entryId)) return;
    removing.current = null;
    const rows = list.current?.querySelectorAll<HTMLLIElement>("li");
    const target = rows?.[Math.min(pending.index, (rows?.length ?? 0) - 1)];
    (target?.querySelector<HTMLButtonElement>("[data-remove]") ?? heading.current)?.focus();
  }, [entries]);
  function remove(entryId: string, index: number) {
    removing.current = { entryId, index };
    engine.removeEntry(entryId);
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
  return <div className={styles.section}>
    <div className={styles.header}>
      <h2 ref={heading} id={headingId} tabIndex={-1} className={styles.heading}>队列（{snapshot.queue.entries.length}）</h2>
      <div className={styles.headerActions}>
        {renderQueueActions === undefined || snapshot.queue.entries.length === 0 ? null : renderQueueActions(snapshot.queue.entries.map((entry) => entry.track))}
        <Button variant="icon" icon={<Trash2 size={16} />} label="清空队列" disabled={snapshot.queue.entries.length === 0} onClick={() => setConfirm(true)} />
        <Button variant="icon" icon={<X size={18} />} label="关闭队列" onClick={onClose} />
      </div>
    </div>
    {snapshot.queue.mode === "shuffle" ? <p className={styles.note}>随机播放时，列表顺序不是实际的播放顺序。</p> : null}
    <ol ref={list} role="list" className={styles.list}>
      {snapshot.queue.entries.map((entry, index) => {
        const current = entry.entryId === snapshot.current?.entryId;
        const failure = snapshot.failures[entry.entryId];
        return <li key={entry.entryId} data-entry-id={entry.entryId} className={styles.row} aria-current={current ? "true" : undefined}>
          <div className={styles.description}><p className={styles.title} title={entry.track.title}><span className={styles.number}>{index + 1}.</span> {entry.track.title}</p><p className={`${styles.secondary} ${styles.metadata}`} title={`${entry.track.artist} — ${entry.track.albumTitle}`}>{entry.track.artist} — {entry.track.albumTitle}</p>
            {current ? <p className={styles.secondary}>当前项 · {statusLabels[snapshot.status]}</p> : null}
            {failure === undefined ? null : <p>{failureLabels[failure]}</p>}
          </div>
          <div className={styles.actions} role="group" aria-label={`第 ${index + 1} 项操作：${entry.track.title}`}>
            <Button variant="icon" icon={<Play size={16} />} label="播放此项" onClick={() => engine.playEntry(entry.entryId)} />
            <Button variant="icon" icon={<ArrowUp size={16} />} label="上移" disabled={index === 0} onClick={() => move(entry.entryId, -1)} />
            <Button variant="icon" icon={<ArrowDown size={16} />} label="下移" disabled={index === snapshot.queue.entries.length - 1} onClick={() => move(entry.entryId, 1)} />
            <Button variant="icon" icon={<X size={16} />} label="移除" data-remove onClick={() => remove(entry.entryId, index)} />
          </div>
          <span className={styles.duration}>{formatDuration(entry.track.durationSeconds)}</span>
        </li>;
      })}
    </ol>
    <ConfirmDialog open={confirm} title="清空播放队列？" confirmLabel="清空队列" onCancel={() => setConfirm(false)} onConfirm={() => { engine.clearQueue(); setConfirm(false); queueMicrotask(() => (heading.current ?? document.getElementById("main"))?.focus()); }}><p>播放将停止，队列中的全部条目会被移除。</p></ConfirmDialog>
  </div>;
}
