import { useViewerScope } from "../../features/auth";
import { LibrarySummary } from "../../features/catalog";
import { RootSummary, ScanSummary } from "../../features/library";
import { MemberSummary } from "../../features/members";
import { Page } from "../../shared/ui";
import styles from "./overview-page.module.css";

/** 概览：只组合有真实来源的数据；管理员另见目录与成员。 */
export function OverviewPage() {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  return (
    <Page title="概览" description="资料库与扫描的当前状态。">
      <div className={styles.grid}>
        <LibrarySummary isAdmin={isAdmin} />
        <ScanSummary canManage={isAdmin} />
        {isAdmin ? <RootSummary /> : null}
        {isAdmin ? <MemberSummary /> : null}
      </div>
    </Page>
  );
}
