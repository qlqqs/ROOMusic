import { Link, Outlet } from "@tanstack/react-router";
import { Page } from "../../shared/ui";
import styles from "./me-layout.module.css";

const tabs = [
  { to: "/me/favorites", label: "收藏" },
  { to: "/me/playlists", label: "歌单" },
  { to: "/me/history", label: "历史" },
] as const;

/**
 * “我的音乐”：只属于当前用户的数据。页签是链接加 aria-current（不是 ARIA 标签页）：选择写进地址，前进、后退与分享都成立；
 * 歌单详情也算在“歌单”页签下。
 */
export function MeLayout() {
  return (
    <Page title="我的音乐">
      <nav aria-label="我的音乐" className={styles.tabs}>
        <ul className={styles.list}>
          {tabs.map((tab) => (
            <li key={tab.to}>
              <Link to={tab.to} search={{}} activeOptions={{ includeSearch: false }} className={styles.tab}>
                {tab.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <Outlet />
    </Page>
  );
}
