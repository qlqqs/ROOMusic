import { Link, useRouterState } from "@tanstack/react-router";
import { Disc3, Heart, LayoutDashboard, MicVocal, type LucideIcon } from "lucide-react";
import { useId, type RefObject } from "react";
import { preferBlankConversation, useStewardEntry } from "../../features/steward";
import { StewardIcon } from "./nav-items";
import { stewardUnreadText, useArtistsAvailable } from "./primary-nav";
import styles from "./tab-bar.module.css";

type Tab = { id: "overview" | "albums" | "artists" | "steward" | "me"; to: "/" | "/albums" | "/artists" | "/steward" | "/me"; label: string; icon: LucideIcon };

// 管家的标签名取自设置里的名字。
const tabs: readonly Tab[] = [
  { id: "overview", to: "/", label: "概览", icon: LayoutDashboard },
  { id: "albums", to: "/albums", label: "专辑", icon: Disc3 },
  { id: "artists", to: "/artists", label: "艺人", icon: MicVocal },
  { id: "steward", to: "/steward", label: "管家", icon: StewardIcon },
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
  if (under(pathname, "/steward")) return "steward";
  if (["/me", "/settings", "/issues", "/claims", "/artist-candidates"].some((base) => under(pathname, base))) return "me";
  return null;
}

/**
 * 窄屏（<768px）底部标签栏：概览、专辑、艺人（后端有艺人能力时）、管家（读到管家设置、且已接上模型或有未读会话时，显示用户起的名字，进整页对话；没接上模型时不占格，从设置进入引导）、我的。当前标签 aria-current="page"，图标与文字用强调色，“我的”的心形填实。
 * 外壳按它的实际高度给迷你播放栏与主内容留位置。
 */
export function TabBar({ ref }: { ref: RefObject<HTMLElement | null> }) {
  const artists = useArtistsAvailable();
  const steward = useStewardEntry();
  const current = useRouterState({ select: (state) => tabAt(state.location.pathname) });
  const unreadId = useId();
  const unread = steward.unread > 0;
  return (
    <nav ref={ref} className={styles.bar} aria-label="主导航">
      <ul className={styles.list}>
        {tabs
          .filter((tab) => (tab.id !== "artists" || artists) && (tab.id !== "steward" || (steward.available && (steward.configured || unread))))
          .map((tab) => (
            <li key={tab.id}>
              {/* 管家：面板没有记着会话时进空白新对话，不转到列表里最近的会话（那是刷新、直接打开地址时的行为）。 */}
              <Link
                to={tab.to}
                className={styles.tab}
                activeOptions={{ exact: true }}
                aria-current={current === tab.id ? "page" : undefined}
                onClick={tab.id === "steward" ? preferBlankConversation : undefined}
                aria-describedby={tab.id === "steward" && unread ? unreadId : undefined}
              >
                <tab.icon size={20} aria-hidden="true" fill={tab.id === "me" && current === "me" ? "currentColor" : "none"} />
                <span className={styles.label}>{tab.id === "steward" ? steward.name : tab.label}</span>
                {/* 有运行结束后还没打开看过的会话（C10）：图标右上角的提示点，数量经 aria-describedby 说明。 */}
                {tab.id === "steward" && unread ? <span className={styles.dot} aria-hidden="true" /> : null}
              </Link>
              {tab.id === "steward" && unread ? (
                <span id={unreadId} hidden>
                  {stewardUnreadText(steward.unread)}
                </span>
              ) : null}
            </li>
          ))}
      </ul>
    </nav>
  );
}
