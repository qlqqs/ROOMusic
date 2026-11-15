import { closestCenter, DndContext, KeyboardSensor, PointerSensor, useSensor, useSensors, type Announcements, type DragEndEvent, type UniqueIdentifier } from "@dnd-kit/core";
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { ArrowDown, ArrowUp, ChevronRight, GripVertical, MoreHorizontal, Play, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { formatDuration, formatSpokenDuration } from "../../../shared/lib/duration";
import { formatCount } from "../../../shared/lib/number";
import { useMediaQuery } from "../../../shared/lib/use-media-query";
import { Button, ConfirmDialog, MenuButton, type MenuAction } from "../../../shared/ui";
import type { PlayerTrack, QueueEntry } from "../engine/queue";
import { usePlayer } from "../player-provider";
import { PlayerArtwork } from "./player-artwork";
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

const REDUCED_MOTION = "(prefers-reduced-motion: reduce)";
/** 移除时行收起的时长，与样式里的动画一致。 */
const REMOVE_MS = 160;

/** 头部统计：“23 首 · 1 小时 32 分钟”；有曲目时长未知时总时长写“以上”。 */
function summaryText(entries: readonly QueueEntry[]): string {
  if (entries.length === 0) return "空";
  const known = entries.filter((entry) => entry.track.durationSeconds !== null);
  const total = known.reduce((sum, entry) => sum + (entry.track.durationSeconds ?? 0), 0);
  const count = `${formatCount(entries.length)} 首`;
  if (known.length === 0) return count;
  return `${count} · ${formatSpokenDuration(total, { floor: known.length < entries.length })}${known.length < entries.length ? "以上" : ""}`;
}

/** “正在播放”封面角上的三根律动条：只在播放中跳动（减少动态效果时静止），装饰性的。 */
function PlayingBars({ playing }: { playing: boolean }) {
  return <span className={styles.bars} data-playing={playing ? "" : undefined} aria-hidden="true"><i /><i /><i /></span>;
}

type RowProps = {
  entry: QueueEntry;
  /** 在完整队列里的下标。 */
  index: number;
  count: number;
  current: boolean;
  secondary: string;
  /** 正在播放的状态（律动条）；只有当前行用。 */
  playing: boolean;
  removing: boolean;
  dimmed?: boolean;
  onPlay: () => void;
  onMove: (toIndex: number) => void;
  onRemove: () => void;
};

/**
 * 队列的一行：把手、封面、标题与艺人（失败时写原因）、时长，行尾“移除”与“更多”（播放此项、上移、下移、移除）。
 * 除当前项外，点行（封面与文字）播放这一项。当前项不能拖（没有把手），但别的行可以越过它。
 */
function QueueRow({ entry, index, count, current, secondary, playing, removing, dimmed = false, onPlay, onMove, onRemove }: RowProps) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: entry.entryId,
    // 当前项不能拖，但仍是放置目标：别的行可以越过它。
    disabled: removing ? true : current ? { draggable: true, droppable: false } : false,
  });
  const title = entry.track.title;
  const actions: MenuAction[] = [
    { id: "play", label: "播放此项", icon: <Play size={16} />, onSelect: onPlay },
    { id: "up", label: "上移", icon: <ArrowUp size={16} />, disabled: index === 0, onSelect: () => onMove(index - 1) },
    { id: "down", label: "下移", icon: <ArrowDown size={16} />, disabled: index === count - 1, onSelect: () => onMove(index + 1) },
    { id: "remove", label: "移除", icon: <Trash2 size={16} />, danger: true, onSelect: onRemove },
  ];
  const body = <>
    <span className={styles.art} aria-hidden="true"><PlayerArtwork track={entry.track} />{current ? <PlayingBars playing={playing} /> : null}</span>
    <span className={styles.text}>
      <span className={styles.title} title={title}>{title}</span>
      <span className={styles.secondary} title={secondary}>{secondary}</span>
    </span>
  </>;
  return <li
    ref={setNodeRef}
    className={styles.row}
    data-entry-id={entry.entryId}
    data-current={current ? "" : undefined}
    data-dragging={isDragging ? "" : undefined}
    data-removing={removing ? "" : undefined}
    data-dimmed={dimmed ? "" : undefined}
    aria-current={current ? "true" : undefined}
    style={{ transform: CSS.Translate.toString(transform), transition }}
  >
    <span className={styles.handle}>
      {current ? null : <button ref={setActivatorNodeRef} type="button" aria-label={`拖动《${title}》调整顺序`} {...attributes} {...listeners}><GripVertical size={16} aria-hidden="true" /></button>}
    </span>
    {current ? <div className={styles.main}>{body}</div> : <button type="button" className={styles.main} aria-label={`播放《${title}》`} onClick={onPlay}>{body}</button>}
    <span className={styles.duration}>{formatDuration(entry.track.durationSeconds)}</span>
    <span className={styles.actions}>
      <Button variant="icon" icon={<X size={16} />} label={`移除《${title}》`} data-remove onClick={onRemove} />
      <MenuButton iconOnly label={`《${title}》的更多操作`} icon={<MoreHorizontal size={16} />} actions={actions} />
    </span>
  </li>;
}

/**
 * 队列抽屉的内容（参考 Spotify 队列面板、Apple Music“接下来”）：头部“队列”与“N 首 · 总时长”、存为歌单、清空、关闭；
 * 下面三段：“已播放 N 首”（默认收起）、“正在播放”、“接下来”。整行点击播放；把手拖动排序（鼠标、触屏、键盘），可以越过正在播放；
 * 放下后先按新顺序显示，服务端的结果到达后以它为准，命令失败时回到原顺序。
 */
export function PlayerQueue({ headingId, onClose, renderQueueActions }: PlayerQueueProps) {
  const { engine, snapshot } = usePlayer();
  const reduced = useMediaQuery(REDUCED_MOTION);
  const [confirm, setConfirm] = useState(false);
  const [showPlayed, setShowPlayed] = useState(false);
  const playedId = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const entries = snapshot.queue.entries;

  // 放下后的临时顺序（条目 ID）：服务端的队列一变就丢掉，以服务端为准。
  const [order, setOrder] = useState<readonly string[] | null>(null);
  const [seenEntries, setSeenEntries] = useState(entries);
  if (seenEntries !== entries) {
    setSeenEntries(entries);
    setOrder(null);
  }
  const byId = new Map(entries.map((entry) => [entry.entryId, entry]));
  const rows = order === null ? entries : order.flatMap((id) => byId.get(id) ?? []);
  const ids = rows.map((entry) => entry.entryId);
  const indexOf = new Map(ids.map((id, index) => [id, index]));
  const currentId = snapshot.current?.entryId ?? null;
  const currentIndex = currentId === null ? -1 : ids.indexOf(currentId);
  const played = currentIndex < 0 ? [] : rows.slice(0, currentIndex);
  const upcoming = currentIndex < 0 ? rows : rows.slice(currentIndex + 1);
  const nowPlaying = currentIndex < 0 ? null : (rows[currentIndex] ?? null);
  const visibleIds = [...(showPlayed ? played : []), ...(nowPlaying === null ? [] : [nowPlaying]), ...upcoming].map((entry) => entry.entryId);

  // 打开时把“正在播放”放到可视区上部；之后切歌时平滑跟随（减少动态效果时直接到位）。
  const scrolled = useRef(false);
  useEffect(() => {
    const row = root.current?.querySelector("[data-current]");
    if (row === null || row === undefined) return;
    row.scrollIntoView({ block: scrolled.current ? "nearest" : "start", behavior: scrolled.current && !reduced ? "smooth" : "auto" });
    scrolled.current = true;
  }, [currentId, reduced]);

  // 移除：行先收起（160ms），再发命令；删除后的焦点落在相邻条目的“移除”（窄屏是“更多”），列表空了回到标题。移除要等服务端的结果，所以等这一项真的消失后再移焦点。
  const [removingIds, setRemovingIds] = useState<ReadonlySet<string>>(new Set());
  const removing = useRef<{ entryId: string; index: number } | null>(null);
  useEffect(() => {
    const pending = removing.current;
    if (pending === null || entries.some((entry) => entry.entryId === pending.entryId)) return;
    removing.current = null;
    const items = root.current?.querySelectorAll<HTMLLIElement>("li[data-entry-id]");
    const target = items?.[Math.min(pending.index, (items?.length ?? 0) - 1)];
    // 窄屏隐藏了“移除”（在菜单里），这时落到同一行的“更多”。
    const remove = target?.querySelector<HTMLButtonElement>("[data-remove]");
    const visible = remove !== null && remove !== undefined && getComputedStyle(remove).display !== "none" ? remove : target?.querySelector<HTMLButtonElement>("button[aria-haspopup='menu']");
    (visible ?? heading.current)?.focus();
  }, [entries]);
  function remove(entryId: string) {
    const visible = Array.from(root.current?.querySelectorAll<HTMLLIElement>("li[data-entry-id]") ?? []);
    removing.current = { entryId, index: Math.max(0, visible.findIndex((row) => row.dataset.entryId === entryId)) };
    setRemovingIds((current) => new Set(current).add(entryId));
    window.setTimeout(() => engine.removeEntry(entryId), reduced ? 0 : REMOVE_MS);
  }

  function move(entryId: string, toIndex: number) {
    const from = ids.indexOf(entryId);
    if (from < 0 || toIndex < 0 || toIndex >= ids.length || from === toIndex) return;
    setOrder(arrayMove(ids, from, toIndex));
    engine.moveEntry(entryId, toIndex).catch(() => setOrder(null));
  }

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const titleOf = (id: UniqueIdentifier) => byId.get(String(id))?.track.title ?? "曲目";
  const positionOf = (id: UniqueIdentifier) => (indexOf.get(String(id)) ?? 0) + 1;
  const announcements: Announcements = {
    onDragStart: ({ active }) => `已拿起《${titleOf(active.id)}》，现在是第 ${positionOf(active.id)} 首。`,
    onDragOver: ({ active, over }) => (over === null ? undefined : `《${titleOf(active.id)}》移到第 ${positionOf(over.id)} 首。`),
    onDragEnd: ({ active, over }) => (over === null ? `《${titleOf(active.id)}》放回原处。` : `《${titleOf(active.id)}》放在第 ${positionOf(over.id)} 首。`),
    onDragCancel: ({ active }) => `已取消，《${titleOf(active.id)}》放回原处。`,
  };
  function dragEnd({ active, over }: DragEndEvent) {
    if (over === null || active.id === over.id) return;
    move(String(active.id), ids.indexOf(String(over.id)));
  }

  const secondaryOf = (entry: QueueEntry, current: boolean) => {
    const failure = snapshot.failures[entry.entryId];
    if (failure !== undefined) return failureLabels[failure];
    if (current && (snapshot.status === "loading" || snapshot.status === "buffering")) return statusLabels[snapshot.status];
    return entry.track.artist;
  };
  const rowOf = (entry: QueueEntry, dimmed = false) => {
    const current = entry.entryId === currentId;
    return <QueueRow
      key={entry.entryId}
      entry={entry}
      index={indexOf.get(entry.entryId) ?? 0}
      count={ids.length}
      current={current}
      secondary={secondaryOf(entry, current)}
      playing={current && snapshot.status === "playing"}
      removing={removingIds.has(entry.entryId)}
      dimmed={dimmed}
      onPlay={() => engine.playEntry(entry.entryId)}
      onMove={(toIndex) => move(entry.entryId, toIndex)}
      onRemove={() => remove(entry.entryId)}
    />;
  };

  return <div ref={root} className={styles.section} data-reduced={reduced ? "" : undefined}>
    <div className={styles.header}>
      <span className={styles.grabber} aria-hidden="true" />
      <div className={styles.headingBlock}>
        <h2 ref={heading} id={headingId} tabIndex={-1} className={styles.heading}>队列</h2>
        <p className={styles.summary}>{summaryText(entries)}</p>
      </div>
      <div className={styles.headerActions}>
        {renderQueueActions === undefined || entries.length === 0 ? null : renderQueueActions(entries.map((entry) => entry.track))}
        <Button variant="icon" icon={<Trash2 size={16} />} label="清空队列" disabled={entries.length === 0} onClick={() => setConfirm(true)} />
        <Button variant="icon" icon={<X size={18} />} label="关闭队列" onClick={onClose} />
      </div>
    </div>
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragEnd={dragEnd}
      accessibility={{ announcements, screenReaderInstructions: { draggable: "按空格键拿起，用上下方向键移动，再按空格键放下；按 Esc 取消。" } }}
    >
      <SortableContext items={visibleIds} strategy={verticalListSortingStrategy}>
        {played.length === 0 ? null : <section className={styles.group}>
          <button type="button" className={styles.toggle} aria-expanded={showPlayed} aria-controls={playedId} onClick={() => setShowPlayed(!showPlayed)}>
            <ChevronRight size={16} aria-hidden="true" className={styles.chevron} />
            已播放 {formatCount(played.length)} 首
          </button>
          {showPlayed ? <ol id={playedId} role="list" className={styles.list}>{played.map((entry) => rowOf(entry, true))}</ol> : null}
        </section>}
        {nowPlaying === null ? null : <section className={styles.group} aria-labelledby={`${playedId}-now`}>
          <h3 id={`${playedId}-now`} className={styles.groupTitle}>正在播放</h3>
          <ol role="list" className={styles.list}>{rowOf(nowPlaying)}</ol>
        </section>}
        <section className={styles.group} aria-labelledby={`${playedId}-next`}>
          <h3 id={`${playedId}-next`} className={styles.groupTitle}>接下来</h3>
          {snapshot.queue.mode === "shuffle" && upcoming.length > 0 ? <p className={styles.note}>随机播放时，列表顺序不是实际的播放顺序。</p> : null}
          {upcoming.length === 0 ? <p className={styles.empty}>队列里没有接下来的曲目</p> : <ol role="list" className={styles.list}>{upcoming.map((entry) => rowOf(entry))}</ol>}
        </section>
      </SortableContext>
    </DndContext>
    <ConfirmDialog open={confirm} title="清空播放队列？" confirmLabel="清空队列" onCancel={() => setConfirm(false)} onConfirm={() => { engine.clearQueue(); setConfirm(false); queueMicrotask(() => (heading.current ?? document.getElementById("main"))?.focus()); }}><p>播放将停止，队列中的全部条目会被移除。</p></ConfirmDialog>
  </div>;
}
