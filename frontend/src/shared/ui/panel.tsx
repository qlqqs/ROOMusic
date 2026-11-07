import type { ReactNode, Ref } from "react";
import { useId } from "react";
import styles from "./panel.module.css";

type PanelProps = {
  title: string;
  /** 标题右侧的操作，例如跳转链接。 */
  action?: ReactNode;
  /**
   * 标题元素的引用。给了它，标题就可以被程序聚焦（tabIndex=-1，不进入 Tab 顺序）：
   * 翻页之类的操作用它把读屏与键盘焦点带到这个区块的顶部。
   */
  headingRef?: Ref<HTMLHeadingElement>;
  children: ReactNode;
};

/** 页面里的一个区块：细线分隔、h2 标题，不做装饰性卡片。 */
export function Panel({ title, action, headingRef, children }: PanelProps) {
  const headingId = useId();
  return (
    <section className={styles.panel} aria-labelledby={headingId} data-panel="">
      <div className={styles.header}>
        <h2 id={headingId} ref={headingRef} tabIndex={headingRef === undefined ? undefined : -1}>
          {title}
        </h2>
        {action === undefined ? null : <div className={styles.action}>{action}</div>}
      </div>
      <div className={styles.body}>{children}</div>
    </section>
  );
}
