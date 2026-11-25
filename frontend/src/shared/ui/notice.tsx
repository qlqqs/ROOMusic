import { Check, CircleAlert, CircleCheck, Hash, Info, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "./button";
import styles from "./notice.module.css";

type NoticeTone = "info" | "success" | "warning" | "error";

type NoticeProps = {
  tone?: NoticeTone;
  title?: string;
  children: ReactNode;
  /** 后端或客户端的请求 ID：标题行尾一个图标按钮，点一下复制（反馈问题时附上）；同时写进浏览器控制台。 */
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
 * 其余用 role="status"。结果就近显示在操作区，不使用全局 toast。请求 ID 平时不显示，由行尾的图标按钮复制。
 */
export function Notice({ tone = "info", title, children, requestId = null, action }: NoticeProps) {
  const Icon = icons[tone];
  useEffect(() => {
    if (requestId) console.info(`请求 ID：${requestId}`);
  }, [requestId]);
  return (
    <div className={styles.notice} data-tone={tone} role={tone === "error" ? "alert" : "status"}>
      <Icon className={styles.icon} size={18} aria-hidden="true" />
      <div className={styles.body}>
        {title === undefined ? null : <p className={styles.title}>{title}</p>}
        <div className={styles.message}>{children}</div>
      </div>
      {requestId ? <RequestIdButton requestId={requestId} /> : null}
      {action === undefined ? null : <div className={styles.action}>{action}</div>}
    </div>
  );
}

const COPIED_MS = 2000;

/**
 * 复制错误编号。经 http 局域网地址打开时浏览器不给剪贴板：改为在按钮旁显示编号并选中，方便手动复制。
 * 复制成功后图标换成对勾两秒，读屏名同步变成“已复制错误编号”。
 */
function RequestIdButton({ requestId }: { requestId: string }) {
  const [state, setState] = useState<"idle" | "copied" | "shown">("idle");
  const text = useRef<HTMLElement>(null);
  useEffect(() => {
    if (state === "shown" && text.current !== null) {
      const selection = window.getSelection();
      selection?.selectAllChildren(text.current);
    }
    if (state !== "copied") return;
    const timer = window.setTimeout(() => setState("idle"), COPIED_MS);
    return () => window.clearTimeout(timer);
  }, [state]);
  const copy = () => {
    if (navigator.clipboard === undefined || !window.isSecureContext) return setState("shown");
    navigator.clipboard.writeText(requestId).then(
      () => setState("copied"),
      () => setState("shown"),
    );
  };
  return (
    <span className={styles.requestId}>
      {state === "shown" ? (
        <code ref={text} className={styles.requestIdText}>
          {requestId}
        </code>
      ) : null}
      <Button
        variant="icon"
        icon={state === "copied" ? <Check size={16} aria-hidden="true" /> : <Hash size={16} aria-hidden="true" />}
        label={state === "copied" ? "已复制错误编号" : "复制错误编号"}
        tooltip={state === "copied" ? "已复制" : `复制错误编号 ${requestId}`}
        onClick={copy}
      />
    </span>
  );
}
