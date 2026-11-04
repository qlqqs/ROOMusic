import { useViewerScope } from "../../features/auth";
import { LibrarySummary } from "../../features/catalog";
import { RecentPlaysPanel } from "../../features/listening";
import { RootSummary, ScanSummary } from "../../features/library";
import { MemberSummary } from "../../features/members";
import { Page } from "../../shared/ui";
import styles from "./overview-page.module.css";

/** 概览：只组合有真实来源的数据；最近播放占满整行排在最前，管理员另见目录与成员。 */
export function OverviewPage() {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  return (
    <Page title="概览">
      <div className={styles.grid}>
        <div className={styles.wide}>
          <RecentPlaysPanel />
        </div>
        <LibrarySummary isAdmin={isAdmin} />
        <ScanSummary canManage={isAdmin} />
        {isAdmin ? <RootSummary /> : null}
        {isAdmin ? <MemberSummary /> : null}
      </div>
    </Page>
  );
}
