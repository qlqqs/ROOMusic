import { Link } from "@tanstack/react-router";
import { ChevronRight, LogOut, Settings } from "lucide-react";
import { useId, type ReactNode } from "react";
import { roleLabels, useLogout, useViewerScope } from "../../features/auth";
import { useCachedTodoCounts } from "../../features/governance";
import { PlaylistCover, usePlaylists } from "../../features/personal";
import { formatCount } from "../../shared/lib/number";
import { Avatar, Button, FailureNotice, Page } from "../../shared/ui";
import { visibleNavGroups, type NavItem } from "../shell/nav-items";
import { NewPlaylistButton } from "../shell/new-playlist-button";
import { useArtistsAvailable } from "../shell/primary-nav";
import styles from "./me-page.module.css";

/**
 * “我的”（/me）：窄屏底部标签栏的第四项，汇集宽屏侧栏里“我的音乐”、歌单、管理员的“管理”分组与侧栏底部的设置、退出登录，
 * 手机上不需要另开菜单。入口与侧栏同源（visibleNavGroups），宽屏侧栏不链接这一页，直接打开地址也能用。
 */
export function MePage() {
  const { viewer } = useViewerScope();
  const artists = useArtistsAvailable();
  const todoCounts = useCachedTodoCounts();
  const logout = useLogout();
  const groups = visibleNavGroups(viewer.role, { artists });
  const myMusic = groups.find((group) => group.label === "我的音乐")?.items.filter((item) => item.playlists !== true) ?? [];
  const manage = groups.find((group) => group.label === "管理")?.items ?? [];
  const roleText = logout.isPending ? "正在退出…" : roleLabels[viewer.role];
  return (
    <Page title="我的">
      <div className={styles.sections}>
        <Link to="/settings/account" className={styles.account} aria-label={`${viewer.username} ${roleText}，账号设置`}>
          <span className={styles.avatar}>
            <Avatar name={viewer.username} />
          </span>
          <span className={styles.who}>
            <span className={styles.name}>{viewer.username}</span>
            <span className={styles.role}>{roleText}</span>
          </span>
          <ChevronRight size={16} aria-hidden="true" className={styles.chevron} />
        </Link>

        <Section title="我的音乐">
          {myMusic.map((item) => (
            <EntryRow key={item.to} item={item} />
          ))}
        </Section>

        <Section title="我的歌单" action={<NewPlaylistButton />}>
          <PlaylistRows />
        </Section>

        {manage.length === 0 ? null : (
          <Section title="管理">
            {manage.map((item) => (
              <EntryRow key={item.to} item={item} count={item.todo === undefined ? null : todoCounts[item.todo]} />
            ))}
          </Section>
        )}

        <section aria-label="设置与退出" className={styles.section}>
          {logout.isError ? (
            <FailureNotice error={logout.error} context="write" title="退出失败" action={<Button onClick={() => logout.mutate()}>重试</Button>} />
          ) : null}
          <ul className={styles.list}>
            <li>
              <Link to="/settings" className={styles.row}>
                <Settings size={18} aria-hidden="true" />
                <span className={styles.label}>设置</span>
              </Link>
            </li>
            <li>
              <button type="button" className={styles.row} aria-busy={logout.isPending || undefined} onClick={() => logout.mutate()}>
                <LogOut size={18} aria-hidden="true" />
                <span className={styles.label}>{logout.isPending ? "正在退出…" : "退出登录"}</span>
              </button>
            </li>
          </ul>
        </section>
      </div>
    </Page>
  );
}

function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={styles.section}>
      <div className={styles.sectionHead}>
        <h2 id={headingId} className={styles.sectionTitle}>
          {title}
        </h2>
        {action}
      </div>
      <ul className={styles.list}>{children}</ul>
    </section>
  );
}

const todoDescriptions = { issues: "个待处理的问题", claims: "个待认领的版本" } as const;

/** 一个入口：图标、名称；待办入口有数量（大于 0）时右侧是计数，可访问名称仍是入口名，数量经 aria-describedby 补充。 */
function EntryRow({ item, count = null }: { item: NavItem; count?: number | null }) {
  const descriptionId = useId();
  const showCount = item.todo !== undefined && count !== null && count > 0;
  return (
    <li>
      <Link to={item.to} className={styles.row} aria-describedby={showCount ? descriptionId : undefined}>
        <item.icon size={18} aria-hidden="true" />
        <span className={styles.label}>{item.label}</span>
        {showCount ? (
          <span className={styles.count} aria-hidden="true">
            {formatCount(count)}
          </span>
        ) : null}
      </Link>
      {showCount && item.todo !== undefined ? (
        <span id={descriptionId} hidden>
          {formatCount(count)} {todoDescriptions[item.todo]}
        </span>
      ) : null}
    </li>
  );
}

/**
 * 歌单：第一页（与侧栏、歌单页共用查询），封面、名称与曲目数；还有更多时末尾是“全部歌单”。
 * 还没读到或读取失败时只留“全部歌单”，进歌单页看完整的状态与重试；没有歌单时写一句并给新建的提示。
 */
function PlaylistRows() {
  const playlists = usePlaylists(1);
  const data = playlists.data;
  if (data !== undefined && data.items.length === 0) {
    return <li className={styles.empty}>还没有歌单，点“＋”新建一个。</li>;
  }
  return (
    <>
      {data?.items.map((playlist) => (
        <li key={playlist.id}>
          <Link to="/me/playlists/$playlistId" params={{ playlistId: playlist.id }} className={styles.row}>
            <span className={styles.cover}>
              <PlaylistCover playlist={playlist} size="thumb" />
            </span>
            <span className={styles.label}>{playlist.name}</span>
            <span className={styles.meta}>{formatCount(playlist.track_count)} 首</span>
          </Link>
        </li>
      ))}
      {data === undefined || data.pagination.total > data.items.length ? (
        <li>
          <Link to="/me/playlists" className={styles.row} data-kind="all">
            <span className={styles.label}>全部歌单</span>
            <ChevronRight size={16} aria-hidden="true" className={styles.chevron} />
          </Link>
        </li>
      ) : null}
    </>
  );
}
