import { BackupFilesSection, BackupRetentionSection, BackupStatusSection } from "../../features/system";
import { Page } from "../../shared/ui";

/** 设置 › 备份：备份状态与立即备份、自动清理规则、备份文件的下载与删除。管理员专用，由路由层的 AdminGate 把关。 */
export function BackupsPage() {
  return (
    <Page title="备份">
      <BackupStatusSection />
      <BackupRetentionSection />
      <BackupFilesSection />
    </Page>
  );
}
