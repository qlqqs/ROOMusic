import { useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { cx } from "../../../shared/lib/class-names";
import { VisuallyHidden } from "../../../shared/ui";
import { albumArtistOf } from "../../catalog";
import type { SearchAlbumItemDTO, SearchTrackItemDTO } from "../api/decoders";
import { useSearchSuggestions } from "../hooks/use-search-suggestions";
import { matchNote } from "../match-note";
import { draftQueryOf, searchPageFor } from "../search-params";
import { trackArtistOf, trackTitleOf } from "../../catalog";
import styles from "./global-search-box.module.css";

/** 输入稍停多久才给建议。 */
export const SUGGESTION_DEBOUNCE_MS = 250;

/** 外壳的“/”快捷键据此找到当前看得见的搜索框（输入框带 data-global-search 属性）。 */
export const GLOBAL_SEARCH_INPUT_SELECTOR = "input[data-global-search]";

type Option =
  | { kind: "album"; item: SearchAlbumItemDTO }
  | { kind: "track"; item: SearchTrackItemDTO }
  | { kind: "all"; q: string };

type GlobalSearchBoxProps = {
  /** 地址里当前的查询词（在结果页时）；变化时输入框跟着地址走，没有提交的草稿不写进地址。 */
  currentQuery: string | undefined;
  /** 导航之后调用（窄屏菜单借此关闭）。 */
  onNavigate?: (() => void) | undefined;
  className?: string | undefined;
};

type PopupPosition = { left: number; top: number; width: number; maxHeight: number };

/** 建议浮层的宽度：至少与输入框一样宽，侧栏里的输入框太窄，放宽到 380px，并留在窗口内。 */
function popupPositionOf(anchor: HTMLElement): PopupPosition {
  const rect = anchor.getBoundingClientRect();
  const width = Math.min(Math.max(rect.width, 380), window.innerWidth - 16);
  const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
  const top = rect.bottom + 4;
  return { left, top, width, maxHeight: Math.max(160, window.innerHeight - top - 8) };
}

/**
 * 全局搜索框（ARIA 组合框）：输入稍停（250 ms，输入法组合中不查询）在下方给出专辑与曲目各前几条建议，最后一项是“查看全部结果”。
 * 上下键在建议间移动，Enter 打开选中的建议、没有选中时进入结果页，Esc 收起建议；焦点始终留在输入框里。
 * 浮层用 fixed 定位，侧栏的滚动区不会把它裁掉，窄屏菜单（模态对话框）里同样可用。
 */
export function GlobalSearchBox({ currentQuery, onNavigate, className }: GlobalSearchBoxProps) {
  const navigate = useNavigate();
  const inputId = useId();
  const listboxId = useId();
  const statusId = useId();
  const anchorRef = useRef<HTMLDivElement>(null);
  const [draft, setDraft] = useState(currentQuery ?? "");
  const [syncedQuery, setSyncedQuery] = useState(currentQuery);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [composing, setComposing] = useState(false);
  const [debounced, setDebounced] = useState<string | null>(null);
  const [tooLong, setTooLong] = useState(false);
  const [position, setPosition] = useState<PopupPosition | null>(null);

  // 地址里的查询词变了（后退、前进、换词）：输入框回到地址里的值。“按属性重置状态”的写法，不需要 effect。
  if (syncedQuery !== currentQuery) {
    setSyncedQuery(currentQuery);
    setDraft(currentQuery ?? "");
    setTooLong(false);
  }

  const draftQuery = draftQueryOf(draft);
  const readyQuery = draftQuery.kind === "ready" ? draftQuery.q : null;

  useEffect(() => {
    if (composing) return;
    const timer = window.setTimeout(() => setDebounced(readyQuery), SUGGESTION_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [readyQuery, composing]);

  const showPopup = open && readyQuery !== null;
  const suggestions = useSearchSuggestions(showPopup ? debounced : null);
  const data = suggestions.data;
  // 防抖还没到、或新词的结果还没回来时，旧建议仍显示，但标为正在更新。
  const settled = data !== undefined && debounced === readyQuery && !suggestions.isPlaceholderData;
  const options: Option[] =
    data === undefined
      ? []
      : [
          ...data.albums.items.map((item): Option => ({ kind: "album", item })),
          ...data.tracks.items.map((item): Option => ({ kind: "track", item })),
        ];
  const hasAny = options.length > 0 || data?.albums.has_more === true || data?.tracks.has_more === true;
  if (readyQuery !== null && (hasAny || suggestions.isError || !settled)) options.push({ kind: "all", q: readyQuery });
  const activeIndex = active < options.length ? active : -1;

  useLayoutEffect(() => {
    if (!showPopup) return;
    const anchor = anchorRef.current;
    if (anchor === null) return;
    const update = () => setPosition(popupPositionOf(anchor));
    update();
    window.addEventListener("resize", update);
    document.addEventListener("scroll", update, true);
    return () => {
      window.removeEventListener("resize", update);
      document.removeEventListener("scroll", update, true);
    };
  }, [showPopup]);

  function close() {
    setOpen(false);
    setActive(-1);
  }

  function submit() {
    const query = draftQueryOf(draft);
    if (query.kind === "empty") return;
    if (query.kind === "too_long") {
      setTooLong(true);
      return;
    }
    close();
    void navigate({ to: "/search", search: searchPageFor(query.q) });
    onNavigate?.();
  }

  function choose(option: Option) {
    if (option.kind === "all") {
      submit();
      return;
    }
    close();
    if (option.kind === "album") {
      const { group, matched_release_id: releaseId } = option.item;
      void navigate({ to: "/albums/$groupId", params: { groupId: group.id }, search: { release: releaseId === group.representative.id ? undefined : releaseId } });
    } else {
      const { item } = option;
      // 打开曲目当前所在的发行，并定位到这一首（滚到、短暂高亮、焦点放在这一行）；不直接播放，不打断正在听的队列。
      void navigate({ to: "/albums/$groupId", params: { groupId: item.group_id }, search: { release: item.release.id, track: item.track_id } });
    }
    onNavigate?.();
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing || composing) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      if (options.length === 0) return;
      const step = event.key === "ArrowDown" ? 1 : -1;
      setActive((activeIndex + step + options.length + (activeIndex < 0 && step < 0 ? 1 : 0)) % options.length);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const option = showPopup && activeIndex >= 0 ? options[activeIndex] : undefined;
      if (option === undefined) submit();
      else choose(option);
    } else if (event.key === "Escape") {
      if (!showPopup) return;
      event.preventDefault();
      close();
    }
  }

  const optionId = (index: number) => `${listboxId}-${index}`;
  const albumCount = data?.albums.items.length ?? 0;

  function renderOption(option: Option, index: number): ReactNode {
    const selected = index === activeIndex;
    const common = {
      id: optionId(index),
      role: "option" as const,
      "aria-selected": selected,
      className: cx(styles.option, selected && styles.active),
      onMouseDown: (event: { preventDefault: () => void }) => event.preventDefault(),
      onMouseMove: () => setActive(index),
      onClick: () => choose(option),
    };
    if (option.kind === "all") {
      return (
        <div key="all" {...common} className={cx(common.className, styles.all)}>
          <Search size={16} aria-hidden="true" />
          <span className={styles.primary}>查看“{option.q}”的全部结果</span>
        </div>
      );
    }
    if (option.kind === "album") {
      const release = option.item.group.representative;
      const artist = albumArtistOf(release);
      const note = matchNote(option.item.match, [release.title, artist]);
      return (
        <div key={`album-${option.item.group.id}`} {...common}>
          <span className={styles.primary}>{release.title}</span>
          <span className={styles.secondary}>{artist}</span>
          {note === null ? null : <span className={styles.note}>{note}</span>}
        </div>
      );
    }
    const { item } = option;
    const title = trackTitleOf(item);
    const artist = trackArtistOf(item);
    const note = matchNote(item.match, [title, artist, item.release.title]);
    return (
      <div key={`track-${item.track_id}`} {...common}>
        <span className={styles.primary}>{title}</span>
        <span className={styles.secondary}>
          {artist} · {item.release.title}
        </span>
        {note === null ? null : <span className={styles.note}>{note}</span>}
      </div>
    );
  }

  const albumOptions = options.slice(0, albumCount);
  const trackOptions = options.filter((option) => option.kind === "track");
  const allOption = options.at(-1)?.kind === "all" ? options.at(-1) : undefined;
  let statusText = "";
  if (showPopup) {
    if (suggestions.isError) statusText = "暂时无法给出建议。";
    else if (!settled && options.length <= 1) statusText = "正在查找…";
    else if (settled && !hasAny) statusText = "没有找到匹配的专辑或曲目。";
  }
  // 读屏只在建议稳定后播报一次（条数或没有结果），输入过程中不逐字打扰。
  const announced = !showPopup ? "" : suggestions.isError ? statusText : !settled ? "" : hasAny ? `${albumOptions.length + trackOptions.length} 条建议` : statusText;

  return (
    <div
      className={cx(styles.box, className)}
      onBlur={(event) => {
        if (!(event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget))) close();
      }}
    >
      <form
        role="search"
        aria-label="全局搜索"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <label htmlFor={inputId} className={styles.label}>
          搜索
        </label>
        <div ref={anchorRef} className={styles.field}>
          <Search size={16} aria-hidden="true" className={styles.icon} />
          <input
            id={inputId}
            data-global-search=""
            className={styles.input}
            type="text"
            inputMode="search"
            enterKeyHint="search"
            autoComplete="off"
            spellCheck={false}
            placeholder="搜索"
            role="combobox"
            aria-expanded={showPopup}
            aria-controls={listboxId}
            aria-autocomplete="list"
            aria-activedescendant={showPopup && activeIndex >= 0 ? optionId(activeIndex) : undefined}
            aria-describedby={tooLong ? statusId : undefined}
            aria-invalid={tooLong ? true : undefined}
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              setTooLong(false);
              setActive(-1);
              setOpen(true);
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={onKeyDown}
            onCompositionStart={() => setComposing(true)}
            onCompositionEnd={() => setComposing(false)}
          />
        </div>
        {tooLong ? (
          <p id={statusId} className={styles.error}>
            搜索词太长了，请短一些。
          </p>
        ) : null}
      </form>
      <VisuallyHidden>
        <span role="status">{announced}</span>
      </VisuallyHidden>
      <div
        id={listboxId}
        role="listbox"
        aria-label="搜索建议"
        aria-busy={showPopup && !settled ? true : undefined}
        className={styles.popup}
        hidden={!showPopup || position === null}
        style={position === null ? undefined : { left: position.left, top: position.top, width: position.width, maxHeight: position.maxHeight }}
      >
        {statusText === "" ? null : (
          <p className={styles.status} aria-hidden="true">
            {statusText}
          </p>
        )}
        {albumOptions.length === 0 ? null : <OptionGroup label="专辑">{albumOptions.map((option, index) => renderOption(option, index))}</OptionGroup>}
        {trackOptions.length === 0 ? null : <OptionGroup label="曲目">{trackOptions.map((option, index) => renderOption(option, albumCount + index))}</OptionGroup>}
        {allOption === undefined ? null : renderOption(allOption, options.length - 1)}
      </div>
    </div>
  );
}

function OptionGroup({ label, children }: { label: string; children: ReactNode }) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className={styles.group}>
      <div id={id} role="presentation" className={styles.groupLabel}>
        {label}
      </div>
      {children}
    </div>
  );
}
