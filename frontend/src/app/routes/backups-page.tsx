import { BackupFilesSection, BackupStatusSection } from "../../features/system";
import { Page } from "../../shared/ui";

/** 系统 › 备份：备份状态、立即备份与备份文件下载。管理员专用，由路由层的 AdminGate 把关。 */
export function BackupsPage() {
  return (
    <Page title="备份">
      <BackupStatusSection />
      <BackupFilesSection />
    </Page>
  );
}
