import type { ComponentPropsWithRef, ReactNode } from "react";
import { cx } from "../lib/class-names";
import styles from "./button.module.css";

type BaseProps = Omit<ComponentPropsWithRef<"button">, "className" | "style" | "children"> & {
  /**
   * 提交中：不设 disabled，焦点留在按钮上；点击被忽略，读屏得到 aria-busy。
   * 文案由调用方改成“正在…”，让进行中的状态不只靠样式。
   */
  busy?: boolean;
};

/**
 * 三种外观：
 * - primary：区块里唯一的主要操作，保留描边的按钮外观；
 * - secondary（默认）：其余文字按钮，无边框，悬停或按下才有浅底；
 * - icon：只有图标，无边框；label 同时是读屏名和悬停提示。
 */
type ButtonProps = BaseProps &
  (
    | { variant?: "primary" | "secondary"; icon?: ReactNode; label?: never; children?: ReactNode }
    | { variant: "icon"; icon: ReactNode; label: string; children?: never }
  );

/** 命令按钮；导航用链接，不用 Button。type 默认为 "button"，避免在表单里意外提交。 */
export function Button({ variant = "secondary", icon, label, busy = false, type = "button", onClick, children, ...rest }: ButtonProps) {
  return (
    <button
      aria-label={label}
      title={label}
      {...rest}
      type={type}
      className={cx(styles.button, styles[variant])}
      aria-busy={busy ? true : undefined}
      aria-disabled={busy ? true : undefined}
      onClick={(event) => {
        if (busy) {
          event.preventDefault();
          return;
        }
        onClick?.(event);
      }}
    >
      {icon === undefined ? null : (
        <span className={styles.glyph} aria-hidden="true">
          {icon}
        </span>
      )}
      {children}
    </button>
  );
}
