import { memo, useEffect, useId, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseMediumDTO, ReleaseTrackDTO } from "../api/decoders";
import { formatDuration } from "../../../shared/lib/duration";
import { mediumHeading, totalDuration, type FieldMarks } from "../format";
import { ArtistNames } from "./artist-names";
import { FieldMarkTag, hasFieldMark } from "./field-mark-tag";
import styles from "./release-media.module.css";

/** 一次定位请求：token 区分不同的到达（同一首曲目在后退、前进时会以新的 token 再来一次）。 */
export type TrackLocate = { trackId: string; token: string };

/** 定位后高亮持续多久；之后只留下可以聚焦的行，没有别的标识。 */
const LOCATE_HIGHLIGHT_MS = 2_500;

type ReleaseMediaProps = {
  /**
   * 每行的动作。动作里带 data-track-play 的控件是这一行的播放按钮：它显示在曲目号的位置（行给出 --track-lead-top、--track-lead-width，
   * 行是它的定位祖先），平时显示曲目号，悬停、聚焦或触屏时换成它；双击这一行等同于点它。
   */
  renderTrackActions?: ((track: ReleaseTrackDTO) => ReactNode) | undefined;
  /** 当前曲目与它的标记文字（由调用方给出，例如“正在播放”“已暂停”）；只给 ID 时写“当前曲目”。标记文字只给读屏，看得见的是曲目号位置的音柱。 */
  currentTrackId?: string | undefined;
  currentTrackLabel?: string | undefined;
  /** 当前曲目正在播放：音柱跳动（减少动态效果时静止）；暂停、加载等其他状态静止。 */
  currentTrackPlaying?: boolean | undefined;
  media: readonly ReleaseMediumDTO[];
  /** 专辑艺人：窄版面的第二行里，曲目艺人与它相同时不重复显示。 */
  albumArtist: string;
  /** 选中的曲目（单击一行选中，右栏显示曲目信息）；onSelectTrack 不给时行不能选中。 */
  selectedTrackId?: string | undefined;
  onSelectTrack?: ((trackId: string) => void) | undefined;
  /** 要定位的曲目（从全局搜索的建议点进来）；处理完（定位了，或这个版本里没有它）调用 onLocated，调用方据此不再重复请求。 */
  locate?: TrackLocate | undefined;
  onLocated?: ((token: string) => void) | undefined;
};

/** 定位到的行：focusToken 变化时滚到这一行并把焦点放上去；highlighted 期间短暂高亮。 */
type Located = { trackId: string; token: string; highlighted: boolean };

type TrackListProps = Pick<ReleaseMediaProps, "albumArtist" | "renderTrackActions" | "currentTrackId" | "currentTrackLabel" | "currentTrackPlaying" | "selectedTrackId" | "onSelectTrack"> & {
  located: Located | null;
  /** 任一曲目有作曲署名时才有“作曲”一栏。 */
  withComposer: boolean;
};

/** 双击发生在这些元素上时不播放：它们有自己的点击含义。 */
const INTERACTIVE = "a, button, input, select, textarea, label, summary, [role='menuitem']";

function isInteractive(target: EventTarget): boolean {
  return target instanceof Element && target.closest(INTERACTIVE) !== null;
}

/** 单击行（不在控件上、没有拖选文字、不是双击的第二下）：选中或取消选中这一行。 */
function selectOnClick(event: MouseEvent<HTMLLIElement>, trackId: string, onSelect: (trackId: string) => void) {
  if (event.detail > 1 || isInteractive(event.target)) return;
  const selection = window.getSelection();
  if (selection !== null && !selection.isCollapsed) return;
  onSelect(trackId);
}

/** 曲目的作曲署名（作曲一栏）。 */
function composersOf(track: ReleaseTrackDTO) {
  return track.credits.filter((credit) => credit.role === "composer");
}

/** 双击的第二次按下不选中文字（否则双击会选中一个词）；按在控件上时不拦，焦点照常移动。 */
function preventWordSelection(event: MouseEvent<HTMLLIElement>) {
  if (event.detail > 1 && !isInteractive(event.target)) event.preventDefault();
}

/**
 * 双击行：等同于点这一行的播放按钮（按钮不可用时什么也不做）。在链接、按钮上双击不触发；
 * 拖选了文字（选区不是空的）时不触发，免得选文字时误播。
 */
function playOnDoubleClick(event: MouseEvent<HTMLLIElement>) {
  if (isInteractive(event.target)) return;
  const selection = window.getSelection();
  if (selection !== null && !selection.isCollapsed) return;
  const play = event.currentTarget.querySelector<HTMLButtonElement>("button[data-track-play]");
  if (play === null || play.disabled) return;
  play.click();
}

/** 曲目号位置的音柱：装饰性的，当前曲目的状态由标题旁只给读屏的文字说明。 */
function Equalizer({ playing }: { playing: boolean }) {
  return (
    <span className={styles.eq} data-playing={playing ? "" : undefined} aria-hidden="true">
      <i />
      <i />
      <i />
    </span>
  );
}

/**
 * memo：当前曲目或它的状态变化时，只有标记变了的行重绘，万首曲目的版本不必整表重绘。
 * 所以每行只拿自己的标记（不是当前曲目 ID），renderTrackActions 与 onSelect 也要求调用方保持同一个引用。
 * 一行一首：曲目号、标题、艺人、（作曲）、动作、时长各占一栏，与表头对齐，过长的文字末尾省略、悬停看全文。
 */
const TrackRow = memo(function TrackRow({
  track,
  albumArtist,
  renderTrackActions,
  withComposer,
  marker,
  playing,
  selected,
  onSelect,
  focusToken,
  highlighted,
}: Pick<ReleaseMediaProps, "albumArtist" | "renderTrackActions"> & {
  track: ReleaseTrackDTO;
  withComposer: boolean;
  marker: string | undefined;
  playing: boolean;
  selected: boolean;
  onSelect: ((trackId: string) => void) | undefined;
  focusToken: string | undefined;
  highlighted: boolean;
}) {
  // 作品里的曲目在作品小标题下面，行内只写乐章名；标题被人工修正或自动补全过（M7 I）时写修正后的标题。
  const marks: FieldMarks = { corrected: track.corrected_fields ?? [], auto: track.auto_fields };
  const titleMarked = hasFieldMark(marks, "title");
  const shownTitle = titleMarked ? track.title : (track.work?.movement ?? track.title);
  const title = shownTitle.trim() === "" ? "（无标题）" : shownTitle;
  const artist = track.artist.trim();
  const composers = composersOf(track);
  const composerText = composers.map((credit) => credit.name).join("、");
  const isCue = track.source_kind === "cue_virtual" || track.cue !== null;
  const rowRef = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (focusToken === undefined) return;
    // 放到下一帧：同一次提交里外壳在路径变化后会把焦点交给主内容区、滚回顶部，定位要在它之后。
    // 先聚焦（让 content-visibility 跳过的分段渲染出来）再滚动；滚动是瞬时的，不做平滑动画。
    const frame = requestAnimationFrame(() => {
      const row = rowRef.current;
      if (row === null) return;
      row.focus({ preventScroll: true });
      row.scrollIntoView({ block: "center" });
    });
    return () => cancelAnimationFrame(frame);
  }, [focusToken]);
  return (
    // 定位到的行可以被程序聚焦（不进 Tab 顺序），读屏随之读出这一行，键盘从这里继续。高亮结束后仍保留，焦点不会因此丢失。
    <li
      ref={rowRef}
      className={styles.track}
      data-track-row=""
      data-context-row=""
      data-has-actions={renderTrackActions === undefined ? undefined : ""}
      data-current={marker === undefined ? undefined : ""}
      data-selected={selected ? "" : undefined}
      tabIndex={focusToken === undefined ? undefined : -1}
      data-located={highlighted ? "" : undefined}
      onMouseDown={renderTrackActions === undefined ? undefined : preventWordSelection}
      onDoubleClick={renderTrackActions === undefined ? undefined : playOnDoubleClick}
      onClick={onSelect === undefined ? undefined : (event) => selectOnClick(event, track.id, onSelect)}
    >
      <span className={styles.number}>
        <span className={styles.numberContent}>{marker === undefined ? (track.position >= 1 ? track.position : "—") : <Equalizer playing={playing} />}</span>
      </span>
      <p className={styles.title} title={title}>
        <span className={styles.titleText}>{title}</span>
        {marker === undefined ? null : <VisuallyHidden>{marker}</VisuallyHidden>}
        {selected ? <VisuallyHidden>，已选中</VisuallyHidden> : null}
        {isCue ? <StatusTag>CUE 分轨</StatusTag> : null}
        {titleMarked ? <FieldMarkTag marks={marks} field="title" /> : null}
      </p>
      {/* 宽版面里 display: contents，两栏直接对齐表头；窄版面里成为标题下一行“艺人 · 作曲”（艺人与专辑艺人相同时不写）。 */}
      <div className={styles.meta}>
        <p className={styles.artist} title={artist} data-same={artist === "" || artist === albumArtist ? "" : undefined}>
          {artist === "" ? null : (
            <>
              <VisuallyHidden>艺人：</VisuallyHidden>
              <ArtistNames text={artist} refs={track.artist_refs} />
              <FieldMarkTag marks={marks} field="artist" />
            </>
          )}
        </p>
        {withComposer ? (
          <p className={styles.composer} title={composerText}>
            {composers.length === 0 ? null : (
              <>
                <VisuallyHidden>作曲：</VisuallyHidden>
                {composers.map((credit, index) => (
                  <span key={`${credit.name}:${index}`}>
                    {index === 0 ? null : "、"}
                    <ArtistNames text={credit.name} refs={credit.artist_refs} />
                  </span>
                ))}
              </>
            )}
          </p>
        ) : null}
      </div>
      {renderTrackActions === undefined ? null : <div className={styles.actions}>{renderTrackActions(track)}</div>}
      <Duration seconds={track.duration_seconds} />
    </li>
  );
});

function Duration({ seconds }: { seconds: number | null }) {
  return (
    <span className={styles.duration}>
      {seconds === null ? (
        <>
          <span aria-hidden="true">{formatDuration(null)}</span>
          <VisuallyHidden>时长未知</VisuallyHidden>
        </>
      ) : (
        formatDuration(seconds)
      )}
    </span>
  );
}

// 跳到某一张介质：把标题滚到视野里并聚焦（读屏随之读出“第 N 张”），不改地址、不增加历史项。
function jumpTo(event: MouseEvent<HTMLAnchorElement>, headingId: string) {
  event.preventDefault();
  const heading = document.getElementById(headingId);
  if (heading === null) return;
  heading.scrollIntoView({ block: "start" });
  heading.focus({ preventScroll: true });
}

/** 一张介质里的曲目按作品分组后的条目：不属于作品的曲目单独一项，相邻且作品序号相同的曲目合成一项。 */
export type TrackListItem =
  | { kind: "track"; track: ReleaseTrackDTO }
  | { kind: "work"; index: number; title: string; tracks: ReleaseTrackDTO[] };

export function groupTracksByWork(tracks: readonly ReleaseTrackDTO[]): TrackListItem[] {
  const items: TrackListItem[] = [];
  for (const track of tracks) {
    const last = items.at(-1);
    if (track.work === null) items.push({ kind: "track", track });
    else if (last?.kind === "work" && last.index === track.work.index) last.tracks.push(track);
    else items.push({ kind: "work", index: track.work.index, title: track.work.title, tracks: [track] });
  }
  return items;
}

/** 表头：只给视觉对齐，读屏从每个单元格前只给读屏的“艺人：”“作曲：”得知栏目。 */
function ColumnHeader({ withComposer, withActions }: { withComposer: boolean; withActions: boolean }) {
  return (
    <div className={styles.columns} aria-hidden="true">
      <span className={styles.number}>#</span>
      <span>标题</span>
      <span>艺人</span>
      {withComposer ? <span>作曲</span> : null}
      {withActions ? <span /> : null}
      <span className={styles.duration}>时长</span>
    </div>
  );
}

function TrackList({ tracks, albumArtist, renderTrackActions, currentTrackId, currentTrackLabel, currentTrackPlaying = false, selectedTrackId, onSelectTrack, located, withComposer }: TrackListProps & { tracks: readonly ReleaseTrackDTO[] }) {
  const idPrefix = useId();
  if (tracks.length === 0) return <p>这一张里没有可显示的曲目。</p>;
  const withActions = renderTrackActions !== undefined;
  const rowOf = (track: ReleaseTrackDTO) => (
    <TrackRow
      key={track.id}
      track={track}
      albumArtist={albumArtist}
      renderTrackActions={renderTrackActions}
      withComposer={withComposer}
      marker={track.id === currentTrackId ? (currentTrackLabel ?? "当前曲目") : undefined}
      playing={track.id === currentTrackId && currentTrackPlaying}
      selected={track.id === selectedTrackId}
      onSelect={onSelectTrack}
      focusToken={located?.trackId === track.id ? located.token : undefined}
      highlighted={located?.trackId === track.id && located.highlighted}
    />
  );
  // 去掉了列表样式的 ol 在 Safari 里会失去列表语义，所以显式写出 role。各层都是 subgrid，栏目与表头对齐。
  return (
    <div className={styles.frame}>
      <div className={styles.grid} data-composer={withComposer ? "" : undefined} data-has-actions={withActions ? "" : undefined}>
        <ColumnHeader withComposer={withComposer} withActions={withActions} />
        <ol className={styles.tracks} role="list">
          {groupTracksByWork(tracks).map((item) => {
            if (item.kind === "track") return rowOf(item.track);
            const titleId = `${idPrefix}-work-${item.index}`;
            return (
              <li key={`work:${item.index}`} className={styles.work}>
                <p className={styles.workHeading}>
                  <span id={titleId} className={styles.workTitle}>
                    {item.title}
                  </span>
                  <Duration seconds={totalDuration([item])} />
                </p>
                <ol className={styles.tracks} role="list" aria-labelledby={titleId}>
                  {item.tracks.map(rowOf)}
                </ol>
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}

/**
 * 介质逐段列出：一张时直接是曲目列表，多于一张时每张一个带标题的分段，顶部有“跳到第 N 张”的页内链接。
 * 每个分段用 content-visibility 跳过屏幕外的布局，万首曲目的版本也能流畅滚动；文字都在 DOM 里，页内查找照常可用。
 * 有定位请求时滚到那一行、把焦点放上去并短暂高亮（减少动态效果时不做渐隐动画）。
 */
export function ReleaseMedia({ media, albumArtist, renderTrackActions, currentTrackId, currentTrackLabel, currentTrackPlaying, selectedTrackId, onSelectTrack, locate, onLocated }: ReleaseMediaProps) {
  const idPrefix = useId();
  // 定位请求按 token 只处理一次（“按属性调整状态”的写法，不需要 effect）：曲目在这个版本里就记下它，不在就什么也不做。
  const [seenToken, setSeenToken] = useState<string | null>(null);
  const [located, setLocated] = useState<Located | null>(null);
  if (locate !== undefined && locate.token !== seenToken) {
    setSeenToken(locate.token);
    const found = media.some((medium) => medium.tracks.some((track) => track.id === locate.trackId));
    if (found) setLocated({ trackId: locate.trackId, token: locate.token, highlighted: true });
  }
  useEffect(() => {
    if (locate !== undefined && locate.token === seenToken) onLocated?.(locate.token);
  }, [locate, seenToken, onLocated]);
  const highlightToken = located?.highlighted === true ? located.token : null;
  useEffect(() => {
    if (highlightToken === null) return;
    const timer = window.setTimeout(() => setLocated((current) => (current === null || current.token !== highlightToken ? current : { ...current, highlighted: false })), LOCATE_HIGHLIGHT_MS);
    return () => window.clearTimeout(timer);
  }, [highlightToken]);

  const withComposer = useMemo(() => media.some((medium) => medium.tracks.some((track) => composersOf(track).length > 0)), [media]);

  if (media.length === 0) return <p>这个版本没有可显示的曲目。</p>;
  const [only] = media;
  const listProps: TrackListProps = { albumArtist, renderTrackActions, currentTrackId, currentTrackLabel, currentTrackPlaying, selectedTrackId, onSelectTrack, located, withComposer };
  if (only !== undefined && media.length === 1) return <TrackList tracks={only.tracks} {...listProps} />;

  const headingIdOf = (index: number) => `${idPrefix}-medium-${index}`;
  return (
    <div className={styles.media}>
      <nav aria-label="介质" className={styles.jump}>
        <span>跳到</span>
        <ul className={styles.jumpList}>
          {media.map((medium, index) => (
            <li key={medium.id}>
              <a href={`#${headingIdOf(index)}`} onClick={(event) => jumpTo(event, headingIdOf(index))}>
                {mediumHeading(medium, index)}
              </a>
            </li>
          ))}
        </ul>
      </nav>
      {media.map((medium, index) => (
        <section key={medium.id} className={styles.medium} aria-labelledby={headingIdOf(index)}>
          <h3 id={headingIdOf(index)} tabIndex={-1} className={styles.heading}>
            {mediumHeading(medium, index)}
            <span className={styles.count}>，{formatCount(medium.tracks.length)} 首</span>
          </h3>
          <TrackList tracks={medium.tracks} {...listProps} />
        </section>
      ))}
    </div>
  );
}
