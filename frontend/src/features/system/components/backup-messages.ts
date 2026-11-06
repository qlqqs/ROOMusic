// 立即备份时另一个进程正在备份（409 backup_busy）：这不是错误，界面用普通提示说这一句，不带请求 ID 与重试。

export const BACKUP_BUSY_MESSAGE = "另一份备份正在进行，稍后再试。";

/** 删除确认框的正文：单份与多份共用这一句。 */
export const BACKUP_DELETE_WARNING = "删除后不能恢复。";

export const BACKUP_DELETE_MESSAGES: Readonly<Record<string, string>> = { backup_directory_not_writable: "备份目录不能写入，没有删除。" };
export const BACKUP_RETENTION_MESSAGES: Readonly<Record<string, string>> = { backup_directory_not_writable: "备份目录不能写入，没有保存。" };

export const retentionDaysError = "填 1 到 3650 之间的整数。";
export const retentionCountError = "填 1 到 1000 之间的整数。";
export const RETENTION_UNREADABLE_MESSAGE = "没能读取当前设置。";

export const deleteSelectedLabel = (count: number) => `删除所选（${count}）`;
export const deleteManyTitle = (count: number) => `删除 ${count} 份备份？`;
export const deleteManyFailedTitle = (count: number) => `有 ${count} 份没能删除`;
export const deletedManyAnnouncement = (count: number) => `已删除 ${count} 份。`;
export const retentionConfirmText = (count: number) => `将删除 ${count} 份旧备份。${BACKUP_DELETE_WARNING}`;
export const retentionSavedText = (deleted: number) => (deleted > 0 ? `已保存，删除了 ${deleted} 份旧备份。` : "已保存。");
export const retentionFailedText = (failed: number) => `有 ${failed} 份旧备份没能删除。`;
