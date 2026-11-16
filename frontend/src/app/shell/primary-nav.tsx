import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useId, useState } from "react";
import type { UserRole } from "../../features/auth";
import { useCatalogState } from "../../features/catalog";
import { useCachedTodoCounts } from "../../features/governance";
import { PlaylistCover, usePlaylists } from "../../features/personal";
import { toggleDock, useDockState, useStewardEntry } from "../../features/steward";
import { formatCount } from "../../shared/lib/number";
import { Button } from "../../shared/ui";
import { NewPlaylistButton } from "./new-playlist-button";
import { visibleNavGroups, type NavItem } from "./nav-items";
import { useNowPlayingOrigin } from "./playback-origin";
import styles from "./primary-nav.module.css";

/** 艺人入口只在后端有艺人能力（整理中或已就绪）时出现；unavailable、状态还没读到或读取失败时不出现。 */
export function useArtistsAvailable(): boolean {
  const artistsState = useCatalogState().data?.artists_state;
  return artistsState === "preparing" || artistsState === "ready";
}

/** 停靠面板的元素 ID：侧栏入口的 aria-controls 指向它。 */
export const STEWARD_DOCK_ID = "steward-dock";
/** 侧栏的管家入口：面板收起后焦点回到这里。 */
export const STEWARD_ENTRY_ID = "steward-entry";

/** 主导航：导航用链接，当前页由 aria-current 表达。 */
export function PrimaryNav({ role }: { role: UserRole }) {
  const artists = useArtistsAvailable();
  const steward = useStewardEntry();
  const todoCounts = useCachedTodoCounts();
  const nowPlaying = useNowPlayingOrigin();
  return (
    <nav className={styles.nav} aria-label="主导航">
      {visibleNavGroups(role, { artists, steward: steward.available }).map((group) => (
        <div key={group.label ?? "default"} className={styles.group}>
          {group.label === null ? null : <p className={styles.groupLabel}>{group.label}</p>}
          <ul className={styles.list}>
            {group.items.map((item) =>
              item.playlists === true ? (
                <PlaylistsNavItem key={item.to} item={item} nowPlaying={nowPlaying} />
              ) : item.steward === true ? (
                <StewardNavItem key={item.to} item={item} name={steward.name} />
              ) : (
                <NavLinkItem
                  key={item.to}
                  item={item}
                  count={item.todo === undefined ? null : todoCounts[item.todo]}
                  marker={item.to === "/me/liked" && nowPlaying.key === "favorites" ? { playing: nowPlaying.playing } : null}
                />
              ),
            )}
          </ul>
        </div>
      ))}
    </nav>
  );
}

const todoDescriptions = { issues: "个待处理的问题", claims: "个待认领的版本" } as const;

const NOW_PLAYING_TEXT = "正在播放这里的曲目";

/** 正在播放的来源旁的三根音柱：真的在播放时跳动，暂停、加载时与减少动态效果时静止。装饰性的，文字经 aria-describedby 给读屏。 */
function NowPlayingBars({ playing }: { playing: boolean }) {
  return (
    <span className={styles.bars} data-playing={playing ? "" : undefined} aria-hidden="true">
      <i />
      <i />
      <i />
    </span>
  );
}

/**
 * 一级导航项。待办入口有数量（大于 0）时右侧是带底色的计数徽标；链接的可访问名称仍是入口名，数量经 aria-describedby 补充（“12 个待处理的问题”）。
 * 正在播放的来源（“我喜欢的音乐”）右侧有音柱，同样经 aria-describedby 说明。
 */
function NavLinkItem({ item, count, marker }: { item: NavItem; count: number | null; marker: { playing: boolean } | null }) {
  const descriptionId = useId();
  const showCount = item.todo !== undefined && count !== null && count > 0;
  const described = showCount || marker !== null;
  return (
    <li>
      <Link to={item.to} className={styles.link} activeOptions={{ exact: item.to === "/" }} aria-describedby={described ? descriptionId : undefined}>
        <item.icon size={18} aria-hidden="true" />
        <span className={styles.label}>{item.label}</span>
        {showCount ? (
          <span className={styles.count} aria-hidden="true">
            {formatCount(count)}
          </span>
        ) : null}
        {marker === null ? null : <NowPlayingBars playing={marker.playing} />}
      </Link>
      {showCount && item.todo !== undefined ? (
        <span id={descriptionId} hidden>
          {formatCount(count)} {todoDescriptions[item.todo]}
        </span>
      ) : marker !== null ? (
        <span id={descriptionId} hidden>
          {NOW_PLAYING_TEXT}
        </span>
      ) : null}
    </li>
  );
}

/**
 * 管家入口：名字取自设置。在别的页面上点它是在当前页面打开或收起右侧面板（按钮，aria-expanded 表达开合）；
 * 在整页对话里它是当前项（链接）。
 */
function StewardNavItem({ item, name }: { item: NavItem; name: string }) {
  const onPage = useRouterState({ select: (state) => state.location.pathname === "/steward" || state.location.pathname.startsWith("/steward/") });
  const dock = useDockState();
  return (
    <li>
      {onPage ? (
        <Link to="/steward" className={styles.link} aria-current="page">
          <item.icon size={18} aria-hidden="true" />
          <span className={styles.label}>{name}</span>
        </Link>
      ) : (
        <button type="button" id={STEWARD_ENTRY_ID} className={styles.link} aria-expanded={dock.open} aria-controls={dock.open ? STEWARD_DOCK_ID : undefined} onClick={toggleDock}>
          <item.icon size={18} aria-hidden="true" />
          <span className={styles.label}>{name}</span>
        </button>
      )}
    </li>
  );
}

// 展开状态是这个浏览器的偏好，默认展开。
const EXPANDED_KEY = "roomusic.nav.playlists-expanded";

type NowPlaying = ReturnType<typeof useNowPlayingOrigin>;

/** “我的歌单”：名字是去歌单列表页的链接，旁边是“新建歌单”（＋）与展开收起按钮。 */
function PlaylistsNavItem({ item, nowPlaying }: { item: NavItem; nowPlaying: NowPlaying }) {
  const listId = useId();
  const [expanded, setExpanded] = useState(() => window.localStorage.getItem(EXPANDED_KEY) !== "0");
  const toggle = () => {
    const next = !expanded;
    window.localStorage.setItem(EXPANDED_KEY, next ? "1" : "0");
    setExpanded(next);
  };
  return (
    <li>
      <div className={styles.row}>
        {/* 歌单详情另有自己的高亮，这里只在歌单列表页时高亮。 */}
        <Link to={item.to} className={styles.link} activeOptions={{ exact: true }}>
          <item.icon size={18} aria-hidden="true" />
          <span className={styles.label}>{item.label}</span>
        </Link>
        <span className={styles.rowActions}>
          <NewPlaylistButton />
          <Button
            variant="icon"
            icon={expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
            label="歌单列表"
            tooltip={expanded ? "收起" : "展开"}
            aria-expanded={expanded}
            aria-controls={expanded ? listId : undefined}
            onClick={toggle}
          />
        </span>
      </div>
      {expanded ? <PlaylistLinks id={listId} nowPlaying={nowPlaying} /> : null}
    </li>
  );
}

/**
 * 展开后的歌单：第一页（最多 20 个，按最近修改倒序），与歌单页共用查询，新建、改名、删除后跟着刷新；还有更多时末尾是“全部歌单”。
 * 每项名称前是 26px 的歌单封面（摘要里已有的 cover_groups，不另发请求），封面与一级导航的图标同一列，不缩进；当前打开的歌单高亮；
 * 正在播放的歌单（本页“播放全部”放进去的那一段还在播）右侧有音柱。
 * 列表元素始终在（加载中、出错或没有歌单时是空的），展开按钮的 aria-controls 才总有指向。
 */
function PlaylistLinks({ id, nowPlaying }: { id: string; nowPlaying: NowPlaying }) {
  const data = usePlaylists(1).data;
  const descriptionId = useId();
  return (
    <ul id={id} className={styles.sublist}>
      {data?.items.map((playlist) => {
        const current = nowPlaying.key === `playlist:${playlist.id}`;
        return (
          <li key={playlist.id}>
            <Link
              to="/me/playlists/$playlistId"
              params={{ playlistId: playlist.id }}
              className={styles.sublink}
              data-now-playing={current ? "" : undefined}
              aria-describedby={current ? descriptionId : undefined}
            >
              <PlaylistCover playlist={playlist} size="thumb" />
              <span className={styles.sublabel}>{playlist.name}</span>
              {current ? <NowPlayingBars playing={nowPlaying.playing} /> : null}
            </Link>
            {current ? (
              <span id={descriptionId} hidden>
                {NOW_PLAYING_TEXT}
              </span>
            ) : null}
          </li>
        );
      })}
      {data !== undefined && data.pagination.total > data.items.length ? (
        <li>
          <Link to="/me/playlists" className={styles.sublink} data-kind="all" activeOptions={{ exact: true }}>
            <span className={styles.sublabel}>全部歌单</span>
          </Link>
        </li>
      ) : null}
    </ul>
  );
}
