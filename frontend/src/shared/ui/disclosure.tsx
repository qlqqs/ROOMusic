import { ChevronRight } from "lucide-react";
import { useState, type ReactNode, type Ref } from "react";
import styles from "./disclosure.module.css";

type DisclosureProps = {
  /** 折叠时就可见的标题，也是可访问名称的主体。 */
  title: string;
  /** 标题后面的补充，例如“（8 项）”；与标题连读。 */
  detail?: string;
  /** 标题行（summary）的引用：折叠面板外的操作结束后可以把焦点带回这里。 */
  summaryRef?: Ref<HTMLElement>;
  /** 展开时才渲染：折叠时没有可聚焦的隐藏控件，内容里的请求也在展开之后才开始。 */
  children: ReactNode;
};

/**
 * 页面底部折叠的区块（来源与证据、修改记录……）。展开状态由 React 持有而不是浏览器：折叠时不渲染内容，
 * 展开时挂载内容；标题行是原生 summary，Enter 与空格展开，焦点环由全局样式提供。换对象时用 key 重新挂载，回到折叠。
 */
export function Disclosure({ title, detail, summaryRef, children }: DisclosureProps) {
  const [open, setOpen] = useState(false);
  return (
    <details className={styles.disclosure} open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary ref={summaryRef} className={styles.summary}>
        <ChevronRight className={styles.chevron} size={18} aria-hidden="true" />
        <span className={styles.title}>{title}</span>
        {detail === undefined ? null : <span className={styles.detail}>{detail}</span>}
      </summary>
      {open ? <div className={styles.body}>{children}</div> : null}
    </details>
  );
}
