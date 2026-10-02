import { RootOperationsSection, RootSection, ScanHistorySection, ScanSection } from "../../features/library";
import { Page } from "../../shared/ui";

/**
 * 扫描目录：先是可操作的扫描与音乐目录，后面是两份记录——扫描记录与目录操作记录。管理员专用，由路由层的 AdminGate 把关。
 * scanPage 是扫描记录的页码（URL 的 scan_page，第 1 页没有该参数）。
 */
export function LibraryPage({ scanPage }: { scanPage: number }) {
  return (
    <Page title="扫描目录" description="管理音乐目录并启动扫描。界面只显示目录名，不显示服务器上的完整路径。">
      <ScanSection />
      <RootSection />
      <ScanHistorySection page={scanPage} />
      <RootOperationsSection />
    </Page>
  );
}
