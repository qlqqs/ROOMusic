import { CircleAlert, CircleCheck, Info, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import styles from "./notice.module.css";

type NoticeTone = "info" | "success" | "warning" | "error";

type NoticeProps = {
  tone?: NoticeTone;
  title?: string;
  children: ReactNode;
  /** 后端或客户端的请求 ID，便于向管理员报告问题。 */
  requestId?: string | null;
  /** 下一步操作，例如“重试”。 */
  action?: ReactNode;
};

const icons = {
  info: Info,
  success: CircleCheck,
  warning: TriangleAlert,
  error: CircleAlert,
} as const;

/**
 * 行内状态或错误。状态由图标与文字表达，不靠颜色；错误用 role="alert" 立即播报，
 * 其余用 role="status"。结果就近显示在操作区，不使用全局 toast。
 */
export function Notice({ tone = "info", title, children, requestId = null, action }: NoticeProps) {
  const Icon = icons[tone];
  return (
    <div className={styles.notice} data-tone={tone} role={tone === "error" ? "alert" : "status"}>
      <Icon className={styles.icon} size={18} aria-hidden="true" />
      <div className={styles.body}>
        {title === undefined ? null : <p className={styles.title}>{title}</p>}
        <div className={styles.message}>{children}</div>
        {requestId ? (
          <p className={styles.meta}>
            请求 ID：<code className={styles.requestId}>{requestId}</code>
          </p>
        ) : null}
      </div>
      {action === undefined ? null : <div className={styles.action}>{action}</div>}
    </div>
  );
}
