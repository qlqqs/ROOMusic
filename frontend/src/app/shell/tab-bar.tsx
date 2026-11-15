import { Link, useRouterState } from "@tanstack/react-router";
import { Disc3, Heart, LayoutDashboard, MicVocal, type LucideIcon } from "lucide-react";
import type { RefObject } from "react";
import { useArtistsAvailable } from "./primary-nav";
import styles from "./tab-bar.module.css";

type Tab = { id: "overview" | "albums" | "artists" | "me"; to: "/" | "/albums" | "/artists" | "/me"; label: string; icon: LucideIcon };

const tabs: readonly Tab[] = [
  { id: "overview", to: "/", label: "概览", icon: LayoutDashboard },
  { id: "albums", to: "/albums", label: "专辑", icon: Disc3 },
  { id: "artists", to: "/artists", label: "艺人", icon: MicVocal },
  { id: "me", to: "/me", label: "我的", icon: Heart },
];

const under = (pathname: string, base: string) => pathname === base || pathname.startsWith(`${base}/`);

/**
 * 地址属于哪个标签：专辑、艺人的详情算在各自标签下；“我的”页里能进去的页面（我的音乐、设置、管理员的待办）都算“我的”。
 * 搜索结果页不属于任何标签。
 */
export function tabAt(pathname: string): Tab["id"] | null {
  if (pathname === "/") return "overview";
  if (under(pathname, "/albums")) return "albums";
  if (under(pathname, "/artists")) return "artists";
  if (["/me", "/settings", "/issues", "/claims", "/artist-candidates"].some((base) => under(pathname, base))) return "me";
  return null;
}

/**
 * 窄屏（<768px）底部标签栏：概览、专辑、艺人（后端有艺人能力时）、我的。当前标签 aria-current="page"，图标与文字用强调色，“我的”的心形填实。
 * 外壳按它的实际高度给迷你播放栏与主内容留位置。
 */
export function TabBar({ ref }: { ref: RefObject<HTMLElement | null> }) {
  const artists = useArtistsAvailable();
  const current = useRouterState({ select: (state) => tabAt(state.location.pathname) });
  return (
    <nav ref={ref} className={styles.bar} aria-label="主导航">
      <ul className={styles.list}>
        {tabs
          .filter((tab) => tab.id !== "artists" || artists)
          .map((tab) => (
            <li key={tab.id}>
              <Link to={tab.to} className={styles.tab} activeOptions={{ exact: true }} aria-current={current === tab.id ? "page" : undefined}>
                <tab.icon size={20} aria-hidden="true" fill={tab.id === "me" && current === "me" ? "currentColor" : "none"} />
                <span>{tab.label}</span>
              </Link>
            </li>
          ))}
      </ul>
    </nav>
  );
}
