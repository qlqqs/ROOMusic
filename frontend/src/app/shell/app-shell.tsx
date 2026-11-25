import { useRouterState } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { lazy, useCallback, useEffect, useId, useRef, useState, type MouseEvent, type ReactNode, type RefObject } from "react";
import { useViewerScope } from "../../features/auth";
import { usePlayerSelector, FullscreenPlayer, PlayerBar, PlayerAnnouncement } from "../../features/player";
import { GLOBAL_SEARCH_INPUT_SELECTOR, GlobalSearchBox } from "../../features/search";
import { ArtistLinksScope } from "../../features/catalog";
import { setDockOpen, useDockState, useStewardEntry } from "../../features/steward";
import { useMediaQuery } from "../../shared/lib/use-media-query";
import { Button, ModalDialog } from "../../shared/ui";
import { CurrentTrackFavorite, SaveQueueAsPlaylist } from "../player-personal-actions";
import type { PlayerTrack } from "../../features/player";
import styles from "./app-shell.module.css";

// 播放器的插槽（模块级函数，引用保持不变）：播放栏的心形放不下提示文字，失败的那句只给读屏；全屏里完整显示。
// 队列抽屉标题旁是“存为歌单”。
const renderBarTrackActions = (track: PlayerTrack) => <CurrentTrackFavorite track={track} message="screen-reader" />;
const renderFullscreenTrackActions = (track: PlayerTrack) => <CurrentTrackFavorite track={track} />;
const renderQueueActions = (tracks: readonly PlayerTrack[]) => <SaveQueueAsPlaylist tracks={tracks} />;
import { LazyPage } from "../routes/lazy-page";
import { Brand } from "./brand";
import { PlaybackOriginProvider } from "./playback-origin";
import { PrimaryNav } from "./primary-nav";
import { StewardHostScope } from "./steward-host";
import { TabBar } from "./tab-bar";
import { useCatalogEpochSync } from "./use-catalog-epoch-sync";
import { STEWARD_DOCK_ID, STEWARD_ENTRY_ID, TopBar } from "./top-bar";

/** 与外壳样式表的断点一致：窄于 768px 时没有侧栏，底部是标签栏。 */
const NARROW = "(max-width: 767px)";
/** 卡片内容加载期间用户没有把焦点挪去别处（仍在顶栏的管家按钮或没有焦点）：卡片就绪后才可以把焦点移进去。 */
function focusStillOnEntry(): boolean {
  const active = document.activeElement;
  return active === null || active === document.body || active.id === STEWARD_ENTRY_ID;
}

// 管家的停靠面板连同 Markdown 依赖按需加载：面板第一次打开时才下载。
const StewardPanel = lazy(() => import("../../features/steward/chat").then((module) => ({ default: module.StewardPanel })));

/** 把元素的实际高度写进外壳的 CSS 变量，随元素尺寸变化更新（迷你播放栏与标签栏的留白据此计算）。 */
function useMeasuredHeight(enabled: boolean, target: RefObject<HTMLElement | null>, frame: RefObject<HTMLElement | null>, variable: string) {
  useEffect(() => {
    if (!enabled) return;
    const element = target.current;
    if (element === null) return;
    const measure = () => frame.current?.style.setProperty(variable, `${element.getBoundingClientRect().height}px`);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [enabled, target, frame, variable]);
}

/** 焦点在可编辑的地方时，“/”就是要输入的字符，不拦截。 */
function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
}

/**
 * 已登录界面的外壳。≥768px：左侧是品牌与导航，主内容上方是顶栏（全局搜索、管家按钮、当前用户的头像菜单），侧栏与主内容各自滚动，播放栏通栏在底部；
 * <768px：顶部栏是品牌与搜索按钮（打开搜索对话框），底部是标签栏（概览、专辑、艺人、管家、我的），迷你播放栏浮在标签栏上方，页面只有一个滚动区。
 * 导航切换页面后，焦点回到主内容区，读屏用户得到页面变化的反馈。
 * 管家卡片（顶栏的管家按钮开合）：≥768px 浮在主内容右侧，从顶栏下方到播放栏上方，不加遮罩、不挤占内容；<768px 与整页对话里不显示；
 * 面板与整页对话的页面上下文、个人数据的刷新由 StewardHostScope 交给管家。
 */
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <PlaybackOriginProvider>
      <StewardHostScope>
        <ShellFrame>{children}</ShellFrame>
      </StewardHostScope>
    </PlaybackOriginProvider>
  );
}

function ShellFrame({ children }: { children: ReactNode }) {
  const { viewer } = useViewerScope();
  // 只订阅“播放栏要不要显示”：外壳不随播放状态、音量等每次变化重绘。队列为空但有可重试的故障时，播放栏仍然显示，重试入口不会消失；
  // 有别的在线设备或别的设备上还有曲目时也显示，设备按钮在播放栏里。
  const hasPlayer = usePlayerSelector((snapshot) => snapshot.queue.entries.length > 0 || snapshot.current !== null || snapshot.fault?.kind === "transient"
    || snapshot.devices.some((row) => !row.isLocal && (row.device.online || (row.zone?.current ?? null) !== null)));
  const narrow = useMediaQuery(NARROW);
  const frameRef = useRef<HTMLDivElement>(null);
  const mainRef = useRef<HTMLElement>(null);
  const playerRef = useRef<HTMLDivElement>(null);
  const tabBarRef = useRef<HTMLElement>(null);
  useMeasuredHeight(hasPlayer, playerRef, frameRef, "--player-measured-height");
  useMeasuredHeight(narrow, tabBarRef, frameRef, "--tabbar-measured-height");
  // 播放栏消失（例如在队列抽屉里清空了队列）时焦点随之消失，放回主内容区。
  const hadPlayer = useRef(hasPlayer);
  useEffect(() => {
    if (hadPlayer.current && !hasPlayer && (document.activeElement === null || document.activeElement === document.body)) mainRef.current?.focus({ preventScroll: true });
    hadPlayer.current = hasPlayer;
  }, [hasPlayer]);
  const [searchOpen, setSearchOpen] = useState(false);
  const searchTitleId = useId();

  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const dock = useDockState();
  const steward = useStewardEntry();
  const showDock = dock.open && steward.available && !narrow && pathname !== "/steward" && !pathname.startsWith("/steward/");
  // 收起卡片：焦点回到顶栏的管家按钮。
  const closeDock = useCallback(() => {
    setDockOpen(false);
    document.getElementById(STEWARD_ENTRY_ID)?.focus();
  }, []);
  // 焦点在卡片里时按 Esc 收起。焦点在卡片外时 Esc 归页面自己（提示、专辑详情等），不同时收起卡片；
  // 输入法组合中的 Esc、菜单与对话框已经处理过的 Esc（preventDefault，或有打开的模态对话框）也不算。
  useEffect(() => {
    if (!showDock) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing || event.defaultPrevented || document.querySelector("dialog[open]") !== null) return;
      if (!(event.target instanceof Node && document.getElementById(STEWARD_DOCK_ID)?.contains(event.target))) return;
      closeDock();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [showDock, closeDock]);
  // 在结果页时，搜索框显示地址里的查询词；别的页面（包括专辑页自己的搜索）不带过来。
  const searchQuery = useRouterState({ select: (state) => (state.location.pathname === "/search" ? state.location.search.q : undefined) });
  useCatalogEpochSync();

  // “/”聚焦搜索框：焦点在输入框、文本区或可编辑内容里时不拦截，带修饰键或输入法组合中也不拦截。
  // 看得见的搜索框优先（宽屏的侧栏、窄屏结果页里的那一个）；窄屏都看不见时打开搜索对话框。
  // 别的模态对话框打开时只找对话框里的搜索框，找不到就不处理。
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
        setSearchOpen(true);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);
  // 搜索对话框打开后焦点直接进输入框。
  const searchContentRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (searchOpen) searchContentRef.current?.querySelector<HTMLInputElement>(GLOBAL_SEARCH_INPUT_SELECTOR)?.focus();
  }, [searchOpen]);
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
    <div ref={frameRef} className={styles.frame} data-player={hasPlayer ? "visible" : undefined} data-steward={showDock ? "open" : undefined}>
      <PlayerAnnouncement />
      <a href="#main" className={styles.skipLink} onClick={skipToMain}>
        跳到主要内容
      </a>
      <header className={styles.topbar}>
        <Brand />
        <Button variant="icon" icon={<Search size={20} />} label="搜索全部音乐" onClick={() => setSearchOpen(true)} />
      </header>
      <div className={styles.sidebar}>
        <Brand />
        <div className={styles.sidebarNav}>
          <PrimaryNav role={viewer.role} />
        </div>
      </div>
      <div className={styles.deskbar}>
        <TopBar searchQuery={searchQuery} />
      </div>
      <main id="main" ref={mainRef} tabIndex={-1} className={styles.main}>
        {children}
      </main>
      {showDock ? (
        <div className={styles.dock}>
          <LazyPage>
            <StewardPanel id={STEWARD_DOCK_ID} onClose={closeDock} canTakeFocus={focusStillOnEntry} />
          </LazyPage>
        </div>
      ) : null}
      {hasPlayer ? <div ref={playerRef} className={styles.player}><PlayerBar renderCurrentTrackActions={renderBarTrackActions} renderQueueActions={renderQueueActions} /></div> : null}
      {narrow ? <TabBar ref={tabBarRef} /> : null}
      <FullscreenPlayer renderCurrentTrackActions={renderFullscreenTrackActions} renderQueueActions={renderQueueActions} />
      <ModalDialog open={searchOpen} titleId={searchTitleId} className={styles.searchSheet} closeOnBackdrop onRequestClose={() => setSearchOpen(false)}>
        <div ref={searchContentRef} className={styles.searchContent}>
          <h2 id={searchTitleId} className={styles.searchTitle}>
            搜索全部音乐
          </h2>
          <GlobalSearchBox currentQuery={searchQuery} onNavigate={() => setSearchOpen(false)} className={styles.searchBox} />
          <Button onClick={() => setSearchOpen(false)}>取消</Button>
        </div>
      </ModalDialog>
    </div>
    </ArtistLinksScope>
  );
}
