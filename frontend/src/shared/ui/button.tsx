import type { ComponentPropsWithRef, ReactNode } from "react";
import { cx } from "../lib/class-names";
import styles from "./button.module.css";

type ButtonVariant = "primary" | "secondary" | "text";

type ButtonProps = Omit<ComponentPropsWithRef<"button">, "className" | "style"> & {
  variant?: ButtonVariant;
  icon?: ReactNode;
  /**
   * 提交中：不设 disabled，焦点留在按钮上；点击被忽略，读屏得到 aria-busy。
   * 文案由调用方改成“正在…”，让进行中的状态不只靠样式。
   */
  busy?: boolean;
};

/** 命令按钮；导航用链接，不用 Button。type 默认为 "button"，避免在表单里意外提交。 */
export function Button({ variant = "secondary", icon, busy = false, type = "button", onClick, children, ...rest }: ButtonProps) {
  return (
    <button
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
        <span className={styles.icon} aria-hidden="true">
          {icon}
        </span>
      )}
      {children}
    </button>
  );
}
