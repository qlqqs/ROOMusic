import { Link, Outlet } from "@tanstack/react-router";
import { Page } from "../../shared/ui";
import styles from "./me-layout.module.css";

/**
 * “我的音乐”：只属于当前用户的数据，其他成员（包括管理员）看不到。页签是链接加 aria-current（不是 ARIA 标签页）：
 * 选择写进地址，前进、后退与分享都成立。目前只有“历史”一个页签，收藏与歌单随 M5 后续子项加入。
 */
export function MeLayout() {
  return (
    <Page title="我的音乐" description="只属于你自己的聆听记录，其他成员和管理员都看不到。">
      <nav aria-label="我的音乐" className={styles.tabs}>
        <ul className={styles.list}>
          <li>
            <Link to="/me/history" search={{}} activeOptions={{ includeSearch: false }} className={styles.tab}>
              历史
            </Link>
          </li>
        </ul>
      </nav>
      <Outlet />
    </Page>
  );
}
