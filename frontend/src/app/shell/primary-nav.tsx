import { Link } from "@tanstack/react-router";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useId, useState } from "react";
import type { UserRole } from "../../features/auth";
import { useCatalogState } from "../../features/catalog";
import { usePlaylists } from "../../features/personal";
import { Button } from "../../shared/ui";
import { visibleNavGroups, type NavItem } from "./nav-items";
import styles from "./primary-nav.module.css";

/** 主导航：导航用链接，当前页由 aria-current 表达。 */
export function PrimaryNav({ role, onNavigate }: { role: UserRole; onNavigate?: () => void }) {
  // 艺人入口只在后端有艺人能力（整理中或已就绪）时出现；unavailable、状态还没读到或读取失败时不出现。
  const artistsState = useCatalogState().data?.artists_state;
  const artists = artistsState === "preparing" || artistsState === "ready";
  return (
    <nav className={styles.nav} aria-label="主导航">
      {visibleNavGroups(role, { artists }).map((group) => (
        <div key={group.label ?? "default"} className={styles.group}>
          {group.label === null ? null : <p className={styles.groupLabel}>{group.label}</p>}
          <ul className={styles.list}>
            {group.items.map((item) =>
              item.playlists === true ? (
                <PlaylistsNavItem key={item.to} item={item} onNavigate={onNavigate} />
              ) : (
                <li key={item.to}>
                  <Link to={item.to} className={styles.link} activeOptions={{ exact: item.to === "/" }} onClick={onNavigate}>
                    <item.icon size={18} aria-hidden="true" />
                    <span>{item.label}</span>
                  </Link>
                </li>
              ),
            )}
          </ul>
        </div>
      ))}
    </nav>
  );
}

// 展开状态是这个浏览器的偏好，默认展开。
const EXPANDED_KEY = "roomusic.nav.playlists-expanded";

/** “我的歌单”：名字是去歌单列表页的链接，旁边的按钮展开或收起下面的歌单。 */
function PlaylistsNavItem({ item, onNavigate }: { item: NavItem; onNavigate: (() => void) | undefined }) {
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
        <Link to={item.to} className={styles.link} activeOptions={{ exact: true }} onClick={onNavigate}>
          <item.icon size={18} aria-hidden="true" />
          <span>{item.label}</span>
        </Link>
        <Button
          variant="icon"
          icon={expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
          label="歌单列表"
          tooltip={expanded ? "收起" : "展开"}
          aria-expanded={expanded}
          aria-controls={expanded ? listId : undefined}
          onClick={toggle}
        />
      </div>
      {expanded ? <PlaylistLinks id={listId} onNavigate={onNavigate} /> : null}
    </li>
  );
}

/**
 * 展开后的歌单：第一页（最多 20 个，按最近修改倒序），与歌单页共用查询，新建、改名、删除后跟着刷新；还有更多时末尾是“全部歌单”。
 * 列表元素始终在（加载中、出错或没有歌单时是空的），展开按钮的 aria-controls 才总有指向。
 */
function PlaylistLinks({ id, onNavigate }: { id: string; onNavigate: (() => void) | undefined }) {
  const data = usePlaylists(1).data;
  return (
    <ul id={id} className={styles.sublist}>
      {data?.items.map((playlist) => (
        <li key={playlist.id}>
          <Link to="/me/playlists/$playlistId" params={{ playlistId: playlist.id }} className={styles.sublink} onClick={onNavigate}>
            {playlist.name}
          </Link>
        </li>
      ))}
      {data !== undefined && data.pagination.total > data.items.length ? (
        <li>
          <Link to="/me/playlists" className={styles.sublink} activeOptions={{ exact: true }} onClick={onNavigate}>
            全部歌单
          </Link>
        </li>
      ) : null}
    </ul>
  );
}
