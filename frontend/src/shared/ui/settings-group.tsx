import { createContext, useContext, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { cx } from "../lib/class-names";
import styles from "./settings-group.module.css";

/**
 * 卡片内容宽度小于它时算窄：行里的控件换到名称下方（CSS 容器查询，见 settings-group.module.css），分段选择改用原生下拉。
 * 两处必须一致；改这里时同步改样式里的 639px。
 */
const NARROW_BELOW = 640;

const NarrowContext = createContext(false);

/** 所在设置卡片是不是窄的；不在卡片里时按宽处理。 */
export function useSettingsNarrow(): boolean {
  return useContext(NarrowContext);
}

type SettingsGroupProps = {
  /** 分组标题（h2）；不给时只有卡片。 */
  title?: string;
  children: ReactNode;
};

/**
 * 设置页的一个分组：h2 标题加一张卡片，卡片里是若干 SettingRow（也可以放表格之类的其他内容），相邻两项之间一条淡分隔线。
 * 卡片宽度用 ResizeObserver 量出，告诉里面的分段选择该不该改成下拉；没有 ResizeObserver（jsdom）时按宽处理。
 */
export function SettingsGroup({ title, children }: SettingsGroupProps) {
  const headingId = useId();
  const card = useRef<HTMLDivElement>(null);
  const [narrow, setNarrow] = useState(false);
  useLayoutEffect(() => {
    const element = card.current;
    if (element === null || typeof ResizeObserver === "undefined") return;
    const measure = () => setNarrow(element.getBoundingClientRect().width < NARROW_BELOW);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return (
    <section className={styles.group} aria-labelledby={title === undefined ? undefined : headingId}>
      {title === undefined ? null : (
        <h2 id={headingId} className={styles.title}>
          {title}
        </h2>
      )}
      <div ref={card} className={styles.card}>
        <NarrowContext value={narrow}>{children}</NarrowContext>
      </div>
    </section>
  );
}

/** 交给控件的编号：控件用 controlId 作 id（名称是指向它的 label），或用 aria-labelledby 指向 labelId；有说明时 aria-describedby 指向 descriptionId。 */
export type SettingRowIds = { controlId: string; labelId: string; descriptionId: string | undefined };

type SettingRowProps = {
  label: ReactNode;
  /** 名称下的一行说明。 */
  description?: ReactNode;
  /** 从属项（例如交叉渐变下的渐变时长）：缩进、字号小一级。所属开关关闭时由调用方不渲染这一行。 */
  nested?: boolean;
  /** 右侧的控件；用函数形式拿到编号，把名称与说明关联到控件上。 */
  children: ReactNode | ((ids: SettingRowIds) => ReactNode);
};

/** 设置卡片里的一行：左边是名称与可选说明，右边是控件、右对齐；卡片窄时控件放不下就换到下一行。 */
export function SettingRow({ label, description, nested = false, children }: SettingRowProps) {
  const base = useId();
  const ids: SettingRowIds = { controlId: `${base}-control`, labelId: `${base}-label`, descriptionId: description === undefined ? undefined : `${base}-description` };
  return (
    <div className={cx(styles.row, nested && styles.nested)}>
      <div className={styles.text}>
        <label id={ids.labelId} htmlFor={ids.controlId} className={styles.label}>
          {label}
        </label>
        {description === undefined ? null : (
          <p id={ids.descriptionId} className={styles.description}>
            {description}
          </p>
        )}
      </div>
      <div className={styles.control}>{typeof children === "function" ? children(ids) : children}</div>
    </div>
  );
}
