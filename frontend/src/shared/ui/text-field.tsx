import { CircleAlert } from "lucide-react";
import type { ComponentPropsWithRef } from "react";
import { useId } from "react";
import styles from "./text-field.module.css";

type TextFieldProps = Omit<ComponentPropsWithRef<"input">, "className" | "style" | "id" | "aria-describedby" | "aria-invalid"> & {
  label: string;
  /** 输入规则或用途说明，始终显示并与输入框关联。 */
  description?: string;
  /** 字段级错误：文字加图标，不只靠颜色；与输入框关联。 */
  error?: string | null;
  /** 标签只给读屏与点击（列表页工具条里的搜索框，用占位文字与旁边的按钮说明用途）；名称不变。 */
  labelHidden?: boolean;
};

export function TextField({ label, description, error, labelHidden = false, ...inputProps }: TextFieldProps) {
  const id = useId();
  const descriptionId = description === undefined ? undefined : `${id}-description`;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [descriptionId, errorId].filter((part) => part !== undefined).join(" ");

  return (
    <div className={styles.field}>
      <label htmlFor={id} className={labelHidden ? styles.hiddenLabel : styles.label}>
        {label}
      </label>
      <input
        {...inputProps}
        id={id}
        className={styles.input}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy === "" ? undefined : describedBy}
      />
      {description === undefined ? null : (
        <p id={descriptionId} className={styles.description}>
          {description}
        </p>
      )}
      {error ? (
        <p id={errorId} className={styles.error}>
          <CircleAlert size={14} aria-hidden="true" />
          <span>{error}</span>
        </p>
      ) : null}
    </div>
  );
}
