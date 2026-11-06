// 立即备份时另一个进程正在备份（409 backup_busy）：这不是错误，界面用普通提示说这一句，不带请求 ID 与重试。

export const BACKUP_BUSY_MESSAGE = "另一份备份正在进行，稍后再试。";
