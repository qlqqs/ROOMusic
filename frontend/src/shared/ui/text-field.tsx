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
};

export function TextField({ label, description, error, ...inputProps }: TextFieldProps) {
  const id = useId();
  const descriptionId = description === undefined ? undefined : `${id}-description`;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [descriptionId, errorId].filter((part) => part !== undefined).join(" ");

  return (
    <div className={styles.field}>
      <label htmlFor={id} className={styles.label}>
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
