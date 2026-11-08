import { useViewerScope } from "../../features/auth";
import { AttentionSummary, LibraryStats, RecentlyAddedPanel } from "../../features/catalog";
import { RecentPlaysPanel } from "../../features/listening";
import { AnalysisSummary, RootSummary, ScanSummary } from "../../features/library";
import { MemberSummary } from "../../features/members";
import { Page } from "../../shared/ui";
import styles from "./overview-page.module.css";

/**
 * 概览：顶部是资料库统计；下面主栏是最近播放与最近新增（没有新增专辑时最近新增整块不出现），右侧窄栏是扫描、音频分析、需要关注，
 * 管理员另见目录与成员。主内容窄时窄栏排到主栏下面。
 */
export function OverviewPage() {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  return (
    <Page title="概览">
      <div className={styles.layout}>
        <LibraryStats isAdmin={isAdmin} />
        <div className={styles.columns}>
          <div className={styles.main}>
            <RecentPlaysPanel />
            <RecentlyAddedPanel />
          </div>
          <div className={styles.side}>
            <ScanSummary canManage={isAdmin} />
            <AnalysisSummary />
            <AttentionSummary />
            {isAdmin ? <RootSummary /> : null}
            {isAdmin ? <MemberSummary /> : null}
          </div>
        </div>
      </div>
    </Page>
  );
}
