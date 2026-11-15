import { ChevronRight } from "lucide-react";
import { useId, type ReactNode } from "react";
import { cx } from "../lib/class-names";
import { Button } from "./button";
import styles from "./status-table.module.css";

/** 一张状态表：卡片外是标题（与 Panel 同样的顶线加 h2），卡片里每行一个对象（StatusRow）。 */
export function StatusTable({ title, children }: { title: string; children: ReactNode }) {
  const headingId = useId();
  return (
    <section className={styles.table} aria-labelledby={headingId}>
      <h2 id={headingId}>{title}</h2>
      <ul role="list" className={styles.rows}>
        {children}
      </ul>
    </section>
  );
}

type StatusRowProps = {
  /** 行首的小图标（装饰）。 */
  icon: ReactNode;
  label: string;
  /** 名称下方的一行说明。 */
  detail?: ReactNode | undefined;
  /** 右侧的状态值（数字、百分比或短词）。 */
  value?: ReactNode | undefined;
  /** attention：需要处理的数字，用强调色（同时有文字，不只靠颜色）。 */
  tone?: "default" | "attention";
  /** 说明下方的细进度条之类。 */
  meter?: ReactNode | undefined;
  /** 整行是链接时由调用方渲染（通常是路由 Link），把 className 与 children 原样放上；行尾出现箭头。 */
  renderLink?: ((props: { className: string; children: ReactNode }) => ReactNode) | undefined;
  /** 不是链接时行尾的操作（例如读取失败后的“重试”）；链接里不能再放按钮。 */
  action?: ReactNode | undefined;
};

/** 状态表的一行：名称与说明在左、状态在右；有去处时整行是一个链接。 */
export function StatusRow({ icon, label, detail, value, tone = "default", meter, renderLink, action }: StatusRowProps) {
  const content = (
    <>
      <span className={styles.icon} aria-hidden="true">
        {icon}
      </span>
      <span className={styles.body}>
        <span className={styles.label}>{label}</span>
        {detail === undefined || detail === null ? null : <span className={styles.detail}>{detail}</span>}
        {meter}
      </span>
      {value === undefined || value === null ? null : <span className={cx(styles.value, tone === "attention" && styles.attention)}>{value}</span>}
      {renderLink !== undefined ? (
        <ChevronRight size={16} aria-hidden="true" className={styles.go} />
      ) : action === undefined ? null : (
        <span className={styles.end}>{action}</span>
      )}
    </>
  );
  return <li className={styles.item}>{renderLink === undefined ? <div className={styles.row}>{content}</div> : renderLink({ className: cx(styles.row, styles.link), children: content })}</li>;
}

/**
 * 还没有数据时的行：读取中写“正在读取…”，第一次读取失败写“无法读取”并给“重试”。
 * 已有旧数据时调用方照常显示旧数据（刷新失败不把整行换成错误）。
 */
export function PendingStatusRow({ icon, label, query }: { icon: ReactNode; label: string; query: { isError: boolean; refetch: () => unknown } }) {
  return query.isError ? (
    <StatusRow icon={icon} label={label} detail="无法读取" action={<Button aria-label={`重试读取${label}`} onClick={() => void query.refetch()}>重试</Button>} />
  ) : (
    <StatusRow icon={icon} label={label} detail="正在读取…" />
  );
}
