import { useViewerScope } from "../../features/auth";
import { AttentionSummary, LibraryStats, RecentlyAddedPanel } from "../../features/catalog";
import { RecentPlaysPanel } from "../../features/listening";
import { AnalysisSummary, RootSummary, ScanSummary } from "../../features/library";
import { MemberSummary } from "../../features/members";
import { Page, StatusTable } from "../../shared/ui";
import { ResumeCard, useRecentCardPlayback } from "./overview-playback";
import { AlbumCardPlay } from "./album-card-play";
import styles from "./overview-page.module.css";
import { CurationSummary, FirstScanHint } from "../../features/curation";

/** 最近播放：卡片上的播放键由这里组合（listening 不导入 player）。 */
function RecentPlays() {
  const playback = useRecentCardPlayback();
  return <RecentPlaysPanel renderCardAction={playback.renderCardAction} status={playback.status} />;
}

/**
 * 概览：主栏从上到下是“继续播放”、最近播放、最近新增（两排都是横排；没有新增专辑时最近新增整块不出现）；
 * 右侧窄栏上面是资料库卡片（专辑、曲目、艺人、作曲家），下面是“资料库状态”表：扫描、音频分析、需要关注，管理员另有音乐目录与成员。
 * 主内容窄时右栏整体排到主栏下面。
 */
export function OverviewPage() {
  const { viewer } = useViewerScope();
  const isAdmin = viewer.role === "admin";
  return (
    <Page title="概览">
      <div className={styles.layout}>
        <div className={styles.columns}>
          <div className={styles.main}>
            <FirstScanHint />
            <ResumeCard />
            <RecentPlays />
            <RecentlyAddedPanel renderPlay={(group) => <AlbumCardPlay group={group} />} />
          </div>
          <div className={styles.side}>
            <LibraryStats isAdmin={isAdmin} />
            <StatusTable title="资料库状态">
              <ScanSummary canManage={isAdmin} />
              <AnalysisSummary />
              <AttentionSummary />
              {isAdmin ? <CurationSummary /> : null}
              {isAdmin ? <RootSummary /> : null}
              {isAdmin ? <MemberSummary /> : null}
            </StatusTable>
          </div>
        </div>
      </div>
    </Page>
  );
}
