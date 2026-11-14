import { useViewerScope } from "../../features/auth";
import { AttentionSummary, LibraryStats, RecentlyAddedPanel } from "../../features/catalog";
import { RecentPlaysPanel } from "../../features/listening";
import { AnalysisSummary, RootSummary, ScanSummary } from "../../features/library";
import { MemberSummary } from "../../features/members";
import { Page, StatusTable } from "../../shared/ui";
import { ResumeCard, useRecentCardPlayback } from "./overview-playback";
import styles from "./overview-page.module.css";

/** 最近播放：卡片上的播放键由这里组合（listening 不导入 player）。 */
function RecentPlays() {
  const playback = useRecentCardPlayback();
  return <RecentPlaysPanel renderCardAction={playback.renderCardAction} status={playback.status} />;
}

/**
 * 概览：主栏从上到下是“继续播放”、最近播放、最近新增（两排都是横排；没有新增专辑时最近新增整块不出现），页底一行资料库账目；
 * 右侧窄栏是一张“资料库状态”表：扫描、音频分析、需要关注，管理员另有音乐目录与成员。
 * 主内容窄时状态表排到主栏下面、账目之前，账目总在最后。
 */
export function OverviewPage() {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  return (
    <Page title="概览">
      <div className={styles.layout}>
        <div className={styles.columns}>
          <div className={styles.main}>
            <ResumeCard />
            <RecentPlays />
            <RecentlyAddedPanel />
          </div>
          <div className={styles.side}>
            <StatusTable title="资料库状态">
              <ScanSummary canManage={isAdmin} />
              <AnalysisSummary />
              <AttentionSummary />
              {isAdmin ? <RootSummary /> : null}
              {isAdmin ? <MemberSummary /> : null}
            </StatusTable>
          </div>
          <div className={styles.ledger}>
            <LibraryStats isAdmin={isAdmin} />
          </div>
        </div>
      </div>
    </Page>
  );
}
