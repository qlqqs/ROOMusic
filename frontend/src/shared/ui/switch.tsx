import type { ComponentPropsWithRef } from "react";
import styles from "./switch.module.css";

type SwitchProps = Omit<ComponentPropsWithRef<"input">, "type" | "role" | "className" | "onChange" | "checked"> & {
  checked: boolean;
  onChange: (checked: boolean) => void;
};

/**
 * 开关：带 role="switch" 的原生复选框，键盘（空格）与读屏的“开/关”都由浏览器提供。
 * 名称由调用方给：SettingRow 的 label 指向它的 id，或者直接给 aria-label。
 */
export function Switch({ checked, onChange, ...rest }: SwitchProps) {
  return <input {...rest} type="checkbox" role="switch" className={styles.switch} checked={checked} onChange={(event) => onChange(event.target.checked)} />;
}
