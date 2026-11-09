import { useRouterState } from "@tanstack/react-router";
import { Menu, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useViewerScope } from "../../features/auth";
import { usePlayerSelector, FullscreenPlayer, PlayerBar, PlayerAnnouncement } from "../../features/player";
import { GLOBAL_SEARCH_INPUT_SELECTOR, GlobalSearchBox } from "../../features/search";
import { ArtistLinksScope } from "../../features/catalog";
import { Button, ModalDialog } from "../../shared/ui";
import { CurrentTrackFavorite, SaveQueueAsPlaylist } from "../player-personal-actions";
import type { PlayerTrack } from "../../features/player";
import styles from "./app-shell.module.css";

// 播放器的插槽（模块级函数，引用保持不变）：播放栏的心形放不下提示文字，失败的那句只给读屏；全屏里完整显示。
// 队列抽屉标题旁是“存为歌单”。
const renderBarTrackActions = (track: PlayerTrack) => <CurrentTrackFavorite track={track} message="screen-reader" />;
const renderFullscreenTrackActions = (track: PlayerTrack) => <CurrentTrackFavorite track={track} />;
const renderQueueActions = (tracks: readonly PlayerTrack[]) => <SaveQueueAsPlaylist tracks={tracks} />;
import { Brand } from "./brand";
import { PrimaryNav } from "./primary-nav";
import { useCatalogEpochSync } from "./use-catalog-epoch-sync";
import { UserMenu } from "./user-menu";

/** 焦点在可编辑的地方时，“/”就是要输入的字符，不拦截。 */
function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
}

/**
 * 已登录界面的外壳。≥768px：左侧导航与主内容各自滚动，当前用户按钮（点开是设置与退出登录）固定在侧栏底部；<768px：顶部栏加“菜单”按钮，导航在模态对话框里，
 * 页面只有一个滚动区。导航切换页面后，焦点回到主内容区，读屏用户得到页面变化的反馈。
 */
export function AppShell({ children }: { children: ReactNode }) {
  const { viewer } = useViewerScope();
  // 只订阅“播放栏要不要显示”：外壳不随播放状态、音量等每次变化重绘。队列为空但有可重试的故障时，播放栏仍然显示，重试入口不会消失；
  // 有别的在线设备或别的设备上还有曲目时也显示，设备按钮在播放栏里。
  const hasPlayer = usePlayerSelector((snapshot) => snapshot.queue.entries.length > 0 || snapshot.current !== null || snapshot.fault?.kind === "transient"
    || snapshot.devices.some((row) => !row.isLocal && (row.device.online || (row.zone?.current ?? null) !== null)));
  const frameRef = useRef<HTMLDivElement>(null);
  const mainRef = useRef<HTMLElement>(null);
  const playerRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!hasPlayer) return;
    const region = playerRef.current;
    if (region === null) return;
    const measure = () => frameRef.current?.style.setProperty("--player-measured-height", `${region.getBoundingClientRect().height}px`);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(region);
    return () => observer.disconnect();
  }, [hasPlayer]);
  // 播放栏消失（例如在队列抽屉里清空了队列）时焦点随之消失，放回主内容区。
  const hadPlayer = useRef(hasPlayer);
  useEffect(() => {
    if (hadPlayer.current && !hasPlayer && (document.activeElement === null || document.activeElement === document.body)) mainRef.current?.focus({ preventScroll: true });
    hadPlayer.current = hasPlayer;
  }, [hasPlayer]);
  const [menuOpen, setMenuOpen] = useState(false);
  const menuTitleId = useId();

  const pathname = useRouterState({ select: (state) => state.location.pathname });
  // 在结果页时，搜索框显示地址里的查询词；别的页面（包括专辑页自己的搜索）不带过来。
  const searchQuery = useRouterState({ select: (state) => (state.location.pathname === "/search" ? state.location.search.q : undefined) });
  useCatalogEpochSync();

  // “/”聚焦搜索框：焦点在输入框、文本区或可编辑内容里时不拦截，带修饰键或输入法组合中也不拦截。
  // 看得见的搜索框优先（宽屏的侧栏、窄屏结果页里的那一个）；窄屏都看不见时打开菜单并聚焦菜单里的搜索框。
  // 别的模态对话框打开时只找对话框里的搜索框，找不到就不处理。
  const focusSearchInMenu = useRef(false);
  const drawerContentRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey || event.isComposing || event.defaultPrevented) return;
      if (isEditableTarget(event.target)) return;
      const openDialog = document.querySelector("dialog[open]");
      const candidates = Array.from(document.querySelectorAll<HTMLInputElement>(GLOBAL_SEARCH_INPUT_SELECTOR)).filter(
        (input) => input.getClientRects().length > 0 && (openDialog === null || openDialog.contains(input)),
      );
      const target = candidates[0];
      if (target !== undefined) {
        event.preventDefault();
        target.focus();
      } else if (openDialog === null) {
        event.preventDefault();
        focusSearchInMenu.current = true;
        setMenuOpen(true);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);
  useEffect(() => {
    if (!menuOpen || !focusSearchInMenu.current) return;
    focusSearchInMenu.current = false;
    drawerContentRef.current?.querySelector<HTMLInputElement>(GLOBAL_SEARCH_INPUT_SELECTOR)?.focus();
  }, [menuOpen]);
  const previousPathname = useRef(pathname);
  useEffect(() => {
    if (previousPathname.current === pathname) return;
    previousPathname.current = pathname;
    if (mainRef.current !== null) mainRef.current.scrollTop = 0;
    window.scrollTo(0, 0);
    mainRef.current?.focus({ preventScroll: true });
  }, [pathname]);

  function skipToMain(event: MouseEvent) {
    event.preventDefault();
    mainRef.current?.focus();
  }

  return (
    // 艺人名能不能做成链接：整个已登录界面只读一次目录状态（主内容与播放器里的艺人名都在范围里）。
    <ArtistLinksScope>
    <div ref={frameRef} className={styles.frame} data-player={hasPlayer ? "visible" : undefined}>
      <PlayerAnnouncement />
      <a href="#main" className={styles.skipLink} onClick={skipToMain}>
        跳到主要内容
      </a>
      <header className={styles.topbar}>
        <Brand />
        <Button icon={<Menu size={18} />} onClick={() => setMenuOpen(true)}>
          菜单
        </Button>
      </header>
      <div className={styles.sidebar}>
        <Brand />
        <GlobalSearchBox currentQuery={searchQuery} />
        <div className={styles.sidebarNav}>
          <PrimaryNav role={viewer.role} />
        </div>
        <div className={styles.identity}>
          <UserMenu />
        </div>
      </div>
      <main id="main" ref={mainRef} tabIndex={-1} className={styles.main}>
        {children}
      </main>
      {hasPlayer ? <div ref={playerRef} className={styles.player}><PlayerBar renderCurrentTrackActions={renderBarTrackActions} renderQueueActions={renderQueueActions} /></div> : null}
      <FullscreenPlayer renderCurrentTrackActions={renderFullscreenTrackActions} renderQueueActions={renderQueueActions} />
      <ModalDialog open={menuOpen} titleId={menuTitleId} className={styles.drawer} onRequestClose={() => setMenuOpen(false)}>
        <div ref={drawerContentRef} className={styles.drawerContent}>
          <div className={styles.drawerHeader}>
            <h2 id={menuTitleId}>菜单</h2>
            <Button icon={<X size={18} />} onClick={() => setMenuOpen(false)}>
              关闭
            </Button>
          </div>
          <GlobalSearchBox currentQuery={searchQuery} onNavigate={() => setMenuOpen(false)} />
          <PrimaryNav role={viewer.role} onNavigate={() => setMenuOpen(false)} />
          <div className={styles.drawerIdentity}>
            <UserMenu onNavigate={() => setMenuOpen(false)} />
          </div>
        </div>
      </ModalDialog>
    </div>
    </ArtistLinksScope>
  );
}
