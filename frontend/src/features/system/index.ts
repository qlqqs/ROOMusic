// system 的公开入口：“设置 › 备份”页的三个区段，运行版本与诊断信息（设置 › 关于），以及页面报错上报。
export { BackupFilesSection } from "./components/backup-files-section";
export { BackupRetentionSection } from "./components/backup-retention-section";
export { BackupStatusSection } from "./components/backup-status-section";
export { reportRenderError, useClientErrorReporting } from "./client-errors";
export { useBuildInfo } from "./hooks/use-build-info";
export { useDiagnosticsExport, type DiagnosticsExport } from "./hooks/use-diagnostics-export";
