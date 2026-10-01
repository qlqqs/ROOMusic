import { RootOperationsSection, RootSection, ScanSection } from "../../features/library";
import { Page } from "../../shared/ui";

/** 扫描目录：依次是扫描、音乐目录与目录操作记录三个区段。管理员专用，由路由层的 AdminGate 把关。 */
export function LibraryPage() {
  return (
    <Page title="扫描目录" description="管理音乐目录并启动扫描。界面只显示目录名，不显示服务器上的完整路径。">
      <ScanSection />
      <RootSection />
      <RootOperationsSection />
    </Page>
  );
}
