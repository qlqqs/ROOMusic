import { RootOperationsSection, RootSection, ScanHistorySection, ScanSection } from "../../features/library";
import { Page } from "../../shared/ui";

/**
 * 音乐目录：先是音乐目录（第一次使用时要做的第一件事），再是扫描，后面是两份记录——扫描记录与目录操作记录。
 * 管理员专用，由路由层的 AdminGate 把关。scanPage 是扫描记录的页码（URL 的 scan_page，第 1 页没有该参数）。
 */
export function LibraryPage({ scanPage }: { scanPage: number }) {
  return (
    <Page title="音乐目录" description="资料库从这些目录读取音乐文件。">
      <RootSection />
      <ScanSection />
      <ScanHistorySection page={scanPage} />
      <RootOperationsSection />
    </Page>
  );
}
