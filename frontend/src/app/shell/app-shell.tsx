import { useRouterState } from "@tanstack/react-router";
import { Menu, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useViewerScope } from "../../features/auth";
import { usePlayerSelector, PlayerBar, PlayerAnnouncement } from "../../features/player";
import { Button, ModalDialog } from "../../shared/ui";
import styles from "./app-shell.module.css";
import { Brand } from "./brand";
import { IdentityPanel } from "./identity-panel";
import { PrimaryNav } from "./primary-nav";

/**
 * 已登录界面的外壳。≥768px：左侧导航与主内容各自滚动；<768px：顶部栏加“菜单”按钮，导航在模态对话框里，
 * 页面只有一个滚动区。导航切换页面后，焦点回到主内容区，读屏用户得到页面变化的反馈。
 */
export function AppShell({ children }: { children: ReactNode }) {
  const { viewer } = useViewerScope();
  // 只订阅“队列是否为空”：外壳不随播放状态、音量等每次变化重绘。
  const hasPlayer = usePlayerSelector((snapshot) => snapshot.queue.entries.length > 0);
  const frameRef = useRef<HTMLDivElement>(null);
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
  const [menuOpen, setMenuOpen] = useState(false);
  const menuTitleId = useId();
  const mainRef = useRef<HTMLElement>(null);

  const pathname = useRouterState({ select: (state) => state.location.pathname });
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
        <PrimaryNav role={viewer.role} />
        <div className={styles.identity}>
          <IdentityPanel />
        </div>
      </div>
      <main id="main" ref={mainRef} tabIndex={-1} className={styles.main}>
        {children}
      </main>
      {hasPlayer ? <div ref={playerRef} className={styles.player}><PlayerBar /></div> : null}
      <ModalDialog open={menuOpen} titleId={menuTitleId} className={styles.drawer} onRequestClose={() => setMenuOpen(false)}>
        <div className={styles.drawerContent}>
          <div className={styles.drawerHeader}>
            <h2 id={menuTitleId}>菜单</h2>
            <Button variant="text" icon={<X size={18} />} onClick={() => setMenuOpen(false)}>
              关闭
            </Button>
          </div>
          <PrimaryNav role={viewer.role} onNavigate={() => setMenuOpen(false)} />
          <IdentityPanel />
        </div>
      </ModalDialog>
    </div>
  );
}
