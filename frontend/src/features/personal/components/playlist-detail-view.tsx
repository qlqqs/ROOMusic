import { closestCenter, DndContext, KeyboardSensor, PointerSensor, useSensor, useSensors, type Announcements, type DragEndEvent, type UniqueIdentifier } from "@dnd-kit/core";
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Link, useNavigate } from "@tanstack/react-router";
import { GripVertical, MoreHorizontal } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { isUuid } from "../../../shared/lib/ids";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, ConfirmDialog, FailureNotice, MenuButton, Notice, Page, Pagination, Skeleton, type MenuAction } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { trackTitleOf, useArtworkTint } from "../../catalog";
import type { ItemMovedDTO, ItemRemovedDTO, ItemRestoredDTO, PlaylistItemDTO, PlaylistItemsPageDTO, PlaylistSummaryDTO } from "../api/decoders";
import type { PlaylistMove } from "../api/playlists";
import type { CommandCallbacks } from "../hooks/use-idempotent-command";
import { PLAYLIST_ITEMS_PAGE_SIZE, usePlaylistCommands, usePlaylistDetail, usePlaylistItems } from "../hooks/use-playlists";
import { overLimitText, playlistDurationText, playlistMessages } from "../playlist-text";
import { CollectionHero, SongsHeading } from "./collection-hero";
import { FavoriteStatusScope } from "./favorite-status-scope";
import { PlaylistCover } from "./playlist-cover";
import { RenamePlaylistDialog } from "./rename-playlist-dialog";
import { TrackTable, TrackTableRow } from "./track-table";
import styles from "./playlist-detail-view.module.css";

type PlaylistDetailViewProps = {
  playlistId: string;
  /** URL 里的页码，从 1 起。 */
  page: number;
  /** 页头的“播放全部”：由 app 组合播放器动作；trailing（“···”）放在同一行按钮后面。 */
  renderPlayAll: (playlist: PlaylistSummaryDTO, trailing: ReactNode) => ReactNode;
  /** 一行的播放、心形与“更多”：由 app 组合；actions 是这个歌单的移动与移除，放进同一个“更多”菜单。 */
  renderTrackActions: (item: PlaylistItemDTO, actions: readonly MenuAction[]) => ReactNode;
};

/** 结果提示：移除（带撤销）、撤销与移动的结果，以及“歌单在别处改过”。 */
type Outcome =
  | { kind: "removed"; title: string; token: string }
  | { kind: "message"; text: string }
  | null;

function NotFound() {
  return (
    <Page title="歌单不存在">
      <p>
        它可能已被删除。 <Link to="/me/playlists">回到歌单</Link>
      </p>
    </Page>
  );
}

export function PlaylistDetailView(props: PlaylistDetailViewProps) {
  return isUuid(props.playlistId) ? <PlaylistDetailContent key={props.playlistId} {...props} /> : <NotFound />;
}

/** 翻页时带上的歌单版本（只对目标页有效）；读到“歌单变了”的 409 就丢掉，从当前版本重新读。自己的修改成功后也丢掉。 */
function useCarriedRevision(page: number) {
  const [carried, setCarried] = useState<{ page: number; revision: number } | null>(null);
  return {
    revision: carried !== null && carried.page === page ? carried.revision : null,
    carry: (target: number, revision: number) => setCarried({ page: target, revision }),
    reset: () => setCarried(null),
    dropIfChanged: (error: unknown) => {
      const changed = isHttpFailure(failureOf(error), 409, "revision_conflict");
      if (changed && carried !== null) setCarried(null);
      return changed;
    },
  };
}

/**
 * 歌单详情：页头（封面、名字与改名、头像与创建日期、“播放全部”与“···”里的删除），“歌曲 N”与总时长；条目按歌单顺序，每行有拖动把手、播放、心形与“更多”
 * （下一首播放、加入队列、添加到歌单…、上移、下移、移到最前、移到最后、从歌单移除）。拖动只在这一页之内，菜单的移动按整个歌单。
 * 同一时间只有一条修改在进行；歌单在别处改过时重新读取并说一句，不静默覆盖。移除后给一个“撤销”。
 */
function PlaylistDetailContent({ playlistId, page, renderPlayAll, renderTrackActions }: PlaylistDetailViewProps) {
  const navigate = useNavigate();
  const detail = usePlaylistDetail(playlistId);
  const carried = useCarriedRevision(page);
  const items = usePlaylistItems(playlistId, page, carried.revision);
  const changed = carried.dropIfChanged(items.error);
  const commands = usePlaylistCommands(playlistId);
  const { viewer } = useViewerScope();
  // 拼图左上那张（单封面时就是显示的那张）的主色。
  const tint = useArtworkTint(detail.data?.cover_groups[0]?.artwork ?? null);
  const [outcome, setOutcome] = useState<Outcome>(null);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [lastCommand, setLastCommand] = useState<"remove" | "restore" | "move" | null>(null);
  // 拖动放下后的临时顺序：只对放下时的这一页数据有效，新数据到达（成功后重新读取）就回到服务端的顺序；失败时立即丢掉。
  const [dropped, setDropped] = useState<{ base: readonly PlaylistItemDTO[]; order: readonly string[] } | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusHeading = useRef(false);
  // 上一条失败命令的“重试”：同一个键、同一个请求，成功后的提示与第一次一样。
  const retryRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (!focusHeading.current) return;
    focusHeading.current = false;
    headingRef.current?.focus();
  });

  const data = items.data;
  const ready = data !== undefined && !items.isPlaceholderData && !items.isError;
  const total = data?.pagination.total ?? 0;
  const pageCount = pageCountOf(total, data?.pagination.page_size ?? PLAYLIST_ITEMS_PAGE_SIZE);
  const goToPage = (target: number, replace = false) => void navigate({ to: "/me/playlists/$playlistId", params: { playlistId }, search: { page: target > 1 ? target : undefined }, replace });
  const outOfRange = useClampedPage({ page, pageCount, ready, onOutOfRange: (target) => goToPage(target, true) });

  const missing = (detail.isError && isHttpFailure(failureOf(detail.error), 404)) || (items.isError && isHttpFailure(failureOf(items.error), 404));
  if (missing) return <NotFound />;

  const busy = [commands.remove, commands.restore, commands.move, commands.rename, commands.destroy].some((command) => command.state.status === "pending");
  const state = data?.playlist;
  const summary = detail.data;
  const name = state?.name ?? summary?.name ?? "歌单";

  const succeeded = () => {
    carried.reset();
  };
  const failed = (error: unknown) => {
    if (isHttpFailure(failureOf(error), 409, "revision_conflict")) carried.reset();
  };

  function move(item: PlaylistItemDTO, request: PlaylistMove, order?: readonly string[]) {
    if (state === undefined || busy) return;
    setLastCommand("move");
    if (order !== undefined && data !== undefined) setDropped({ base: data.items, order });
    const title = trackTitleOf(item.track);
    const callbacks: CommandCallbacks<ItemMovedDTO> = {
      onSucceeded: (result) => {
        succeeded();
        // 拖动的结果眼前就看得到；菜单的移动可能移出这一页，说一句移到了哪里。
        if (request.action === "before") return;
        const targetPage = Math.ceil(result.moved.position / PLAYLIST_ITEMS_PAGE_SIZE);
        setOutcome({ kind: "message", text: `《${title}》已移到第 ${formatCount(result.moved.position)} 首${targetPage === page ? "" : `（第 ${formatCount(targetPage)} 页）`}。` });
      },
      onFailed: (error) => {
        setDropped(null);
        failed(error);
      },
    };
    retryRef.current = () => commands.move.retry(callbacks);
    commands.move.run({ expectedRevision: state.revision, itemId: item.id, move: request }, callbacks);
  }

  function remove(item: PlaylistItemDTO) {
    if (state === undefined || busy) return;
    setLastCommand("remove");
    const title = trackTitleOf(item.track);
    const callbacks: CommandCallbacks<ItemRemovedDTO> = {
      onSucceeded: (result) => {
        succeeded();
        setOutcome({ kind: "removed", title, token: result.undo.token });
        // 打开菜单的那一行已经不在了：焦点回到区块标题，紧接着 Tab 就是“撤销”。
        focusHeading.current = true;
      },
      onFailed: failed,
    };
    retryRef.current = () => commands.remove.retry(callbacks);
    commands.remove.run({ expectedRevision: state.revision, itemId: item.id }, callbacks);
  }

  function restore(token: string, title: string) {
    if (busy) return;
    setLastCommand("restore");
    const callbacks: CommandCallbacks<ItemRestoredDTO> = {
      onSucceeded: (result) => {
        succeeded();
        const position = result.item.position;
        const targetPage = Math.ceil(position / PLAYLIST_ITEMS_PAGE_SIZE);
        setOutcome({
          kind: "message",
          text: result.outcome === "already_present" ? `《${title}》已在歌单里。` : `已放回《${title}》，在第 ${formatCount(position)} 首。`,
        });
        if (targetPage !== page) goToPage(targetPage);
      },
    };
    retryRef.current = () => commands.restore.retry(callbacks);
    commands.restore.run({ token }, callbacks);
  }

  const actionsOf = (item: PlaylistItemDTO): MenuAction[] => {
    const count = state?.track_count ?? total;
    const first = item.position <= 1;
    const last = item.position >= count;
    return [
      { id: "up", label: "上移", disabled: busy || first, onSelect: () => move(item, { action: "up" }) },
      { id: "down", label: "下移", disabled: busy || last, onSelect: () => move(item, { action: "down" }) },
      { id: "first", label: "移到最前", disabled: busy || first, onSelect: () => move(item, { action: "first" }) },
      { id: "last", label: "移到最后", disabled: busy || last, onSelect: () => move(item, { action: "last" }) },
      { id: "remove", label: "从歌单移除", disabled: busy, onSelect: () => remove(item) },
    ];
  };

  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };

  const commandFailure = lastCommand === null ? null : commands[lastCommand].state;
  const retryLast = () => retryRef.current?.();

  const trackCount = state?.track_count ?? summary?.track_count;
  // 摘要到达之前用 Page 的默认页头（标题“歌单”）。
  const header =
    summary === undefined ? undefined : (
      <CollectionHero
        title={name}
        cover={<PlaylistCover playlist={{ name, cover_groups: summary.cover_groups }} />}
        tint={tint}
        onRename={state === undefined ? undefined : () => setRenaming(true)}
        renameBusy={commands.rename.state.status === "pending"}
        owner={viewer.username}
        createdAt={summary.created_at}
        actions={renderPlayAll(
          summary,
          <MenuButton
            iconOnly
            icon={<MoreHorizontal size={16} />}
            label="歌单操作"
            actions={[{ id: "delete", label: "删除歌单", disabled: state === undefined, onSelect: () => setDeleting(true) }]}
          />,
        )}
      />
    );

  return (
    <>
      <Page title={name} header={header}>
        <div className={styles.body}>
          {summary === undefined && detail.isError ? <FailureNotice error={detail.error} action={<Button onClick={() => void detail.refetch()}>重试</Button>} /> : null}
          <SongsHeading
            headingRef={headingRef}
            count={trackCount ?? null}
            detail={summary === undefined ? "" : playlistDurationText({ ...summary, track_count: trackCount ?? summary.track_count })}
          />
          {state !== undefined && state.over_limit_count > 0 ? <Notice tone="warning">{overLimitText(state.over_limit_count)}</Notice> : null}
          {outcome?.kind === "removed" ? (
            <Notice
              tone="success"
              action={
                <Button busy={commands.restore.state.status === "pending"} onClick={() => restore(outcome.token, outcome.title)}>
                  {commands.restore.state.status === "pending" ? "正在撤销…" : "撤销"}
                </Button>
              }
            >
              已移除《{outcome.title}》
            </Notice>
          ) : null}
          {outcome?.kind === "message" ? <Notice tone="success">{outcome.text}</Notice> : null}
          {changed ? <p role="status" className={styles.note}>歌单有变化，正在重新读取。</p> : null}
          {commandFailure?.status === "failed" ? (
            <FailureNotice
              error={commandFailure.error}
              context="write"
              messages={playlistMessages}
              action={commandFailure.uncertain ? <Button onClick={retryLast}>重试</Button> : undefined}
            />
          ) : null}
          {items.isError && !changed ? <FailureNotice error={items.error} action={<Button onClick={() => void items.refetch()}>重试</Button>} /> : null}
          {data === undefined || outOfRange ? (
            items.isError ? null : <Skeleton label="正在加载歌单" lines={6} />
          ) : total === 0 ? (
            <p>歌单里还没有曲目。</p>
          ) : data.items.length === 0 ? (
            <p>
              这一页没有曲目。{" "}
              <Link to="/me/playlists/$playlistId" params={{ playlistId }} search={{}}>
                回到第一页
              </Link>
            </p>
          ) : (
            <div className={styles.results} aria-busy={items.isPlaceholderData || changed}>
              <PlaylistItems
                data={data}
                order={dropped !== null && dropped.base === data.items ? dropped.order : null}
                busy={busy}
                renderTrackActions={renderTrackActions}
                actionsOf={actionsOf}
                onDrop={(item, order, beforeItemId) => move(item, { action: "before", beforeItemId }, order)}
              />
              <Pagination
                label="歌单分页"
                page={page}
                pageCount={pageCount}
                renderLink={(target, linkProps) => (
                  <Link
                    to="/me/playlists/$playlistId"
                    params={{ playlistId }}
                    search={{ page: target > 1 ? target : undefined }}
                    onClick={() => {
                      carried.carry(target, data.playlist.revision);
                      showTop();
                    }}
                    {...linkProps}
                  />
                )}
              />
            </div>
          )}
        </div>
      </Page>
      {state === undefined ? null : (
        <RenamePlaylistDialog
          open={renaming}
          name={state.name}
          revision={state.revision}
          command={commands.rename}
          onClose={() => {
            setRenaming(false);
          }}
        />
      )}
      <ConfirmDialog
        open={deleting}
        title="删除歌单？"
        confirmLabel="删除歌单"
        pending={commands.destroy.state.status === "pending"}
        error={
          commands.destroy.state.status === "failed" ? (
            <FailureNotice
              error={commands.destroy.state.error}
              context="write"
              messages={playlistMessages}
              action={commands.destroy.state.uncertain ? <Button onClick={() => commands.destroy.retry({ onSucceeded: () => void navigate({ to: "/me/playlists" }) })}>重试</Button> : undefined}
            />
          ) : undefined
        }
        onCancel={() => {
          commands.destroy.dismiss();
          setDeleting(false);
        }}
        onConfirm={() => {
          if (state === undefined) return;
          commands.destroy.run({ expectedRevision: state.revision }, { onSucceeded: () => void navigate({ to: "/me/playlists" }) });
        }}
      >
        <p>歌单《{name}》会被删除，不能恢复。</p>
      </ConfirmDialog>
    </>
  );
}

type PlaylistItemsProps = {
  data: PlaylistItemsPageDTO;
  /** 放下后的临时顺序（条目 ID）；没有时按服务端的顺序。 */
  order: readonly string[] | null;
  /** 有修改正在进行：不能开始新的拖动。 */
  busy: boolean;
  renderTrackActions: PlaylistDetailViewProps["renderTrackActions"];
  actionsOf: (item: PlaylistItemDTO) => MenuAction[];
  /** 拖动放下：beforeItemId 是放下后紧跟在它后面的条目（本页最后时是下一页的第一项，整个歌单的最后时是 null）。 */
  onDrop: (item: PlaylistItemDTO, order: readonly string[], beforeItemId: string | null) => void;
};

/**
 * 一页条目与拖动排序。鼠标与触屏按住把手拖动，键盘在把手上按空格拿起、方向键移动、空格放下（Esc 取消），过程由读屏播报。
 * 放下后先按新顺序显示，服务端的结果到达后以它为准；失败时回到原来的顺序。
 */
function PlaylistItems({ data, order, busy, renderTrackActions, actionsOf, onDrop }: PlaylistItemsProps) {
  const sensors = useSensors(useSensor(PointerSensor), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const byId = useMemo(() => new Map(data.items.map((item) => [item.id, item])), [data.items]);
  const rows = order === null ? data.items : order.flatMap((id) => byId.get(id) ?? []);
  const ids = rows.map((item) => item.id);
  const trackIds = useMemo(() => data.items.map((item) => item.track.track_id), [data.items]);
  const firstPosition = data.items[0]?.position ?? 1;
  const titleOf = (id: UniqueIdentifier) => {
    const item = byId.get(String(id));
    return item === undefined ? "曲目" : trackTitleOf(item.track);
  };
  const positionOf = (id: UniqueIdentifier) => firstPosition + Math.max(0, ids.indexOf(String(id)));
  const announcements: Announcements = {
    onDragStart: ({ active }) => `已拿起《${titleOf(active.id)}》，现在是第 ${positionOf(active.id)} 首。`,
    onDragOver: ({ active, over }) => (over === null ? undefined : `《${titleOf(active.id)}》移到第 ${positionOf(over.id)} 首。`),
    onDragEnd: ({ active, over }) => (over === null ? `《${titleOf(active.id)}》放回原处。` : `《${titleOf(active.id)}》放在第 ${positionOf(over.id)} 首。`),
    onDragCancel: ({ active }) => `已取消，《${titleOf(active.id)}》放回原处。`,
  };
  function dragEnd({ active, over }: DragEndEvent) {
    if (over === null || active.id === over.id) return;
    const from = ids.indexOf(String(active.id));
    const to = ids.indexOf(String(over.id));
    const item = byId.get(String(active.id));
    if (from < 0 || to < 0 || item === undefined) return;
    const next = arrayMove(ids, from, to);
    onDrop(item, next, next[to + 1] ?? data.page_after_item_id);
  }
  return (
    <FavoriteStatusScope trackIds={trackIds}>
      {/* DndContext 在表格外面：它在自己的位置渲染读屏说明与播报区，放进表体会让 rowgroup 里出现不是 row 的元素。 */}
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={dragEnd}
        accessibility={{ announcements, screenReaderInstructions: { draggable: "按空格键拿起，用上下方向键移动，再按空格键放下；按 Esc 取消。" } }}
      >
        <TrackTable label="歌单曲目" sortable>
          <SortableContext items={ids} strategy={verticalListSortingStrategy}>
            {rows.map((item) => (
              <PlaylistRow key={item.id} item={item} disabled={busy} actions={renderTrackActions(item, actionsOf(item))} />
            ))}
          </SortableContext>
        </TrackTable>
      </DndContext>
    </FavoriteStatusScope>
  );
}

function PlaylistRow({ item, disabled, actions }: { item: PlaylistItemDTO; disabled: boolean; actions: ReactNode }) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id: item.id, disabled });
  return (
    <TrackTableRow
      ref={setNodeRef}
      track={item.track}
      number={item.position}
      actions={actions}
      style={{ transform: CSS.Translate.toString(transform), transition }}
      dragging={isDragging}
      handle={
        // 修改进行中不能开始新的拖动，但把手不设 disabled：键盘放下后焦点留在把手上，不丢到页面开头。
        <button ref={setActivatorNodeRef} type="button" aria-label={`拖动《${trackTitleOf(item.track)}》调整顺序`} {...attributes} {...listeners}>
          <GripVertical size={18} aria-hidden="true" />
        </button>
      }
    />
  );
}
