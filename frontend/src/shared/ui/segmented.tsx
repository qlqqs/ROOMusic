import { useId } from "react";
import { useSettingsNarrow } from "./settings-group";
import styles from "./segmented.module.css";

export type SegmentedOption<T extends string | number> = { value: T; label: string };

type SegmentedProps<T extends string | number> = {
  options: readonly SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  disabled?: boolean;
  /** 名称所在元素的 id（SettingRow 给的 labelId）；单选组与下拉都用它作可访问名称。 */
  labelId: string;
  /** 下拉形态时作为下拉的 id，SettingRow 的 label 指向它。 */
  controlId?: string | undefined;
  descriptionId?: string | undefined;
};

/**
 * 分段选择：宽卡片里是一组相连的单选按钮，窄卡片（见 SettingsGroup）里改用原生 <select>，选项、取值与可访问名称相同。
 * 同一时间只渲染一种，读屏与 Tab 顺序里不会出现两份。
 */
export function Segmented<T extends string | number>({ options, value, onChange, disabled = false, labelId, controlId, descriptionId }: SegmentedProps<T>) {
  const narrow = useSettingsNarrow();
  const name = useId();
  if (narrow) {
    return (
      <select
        id={controlId}
        className={styles.select}
        aria-labelledby={labelId}
        aria-describedby={descriptionId}
        value={options.findIndex((option) => option.value === value)}
        disabled={disabled}
        onChange={(event) => {
          const picked = options[Number(event.target.value)];
          if (picked !== undefined) onChange(picked.value);
        }}
      >
        {options.map((option, index) => (
          <option key={option.value} value={index}>
            {option.label}
          </option>
        ))}
      </select>
    );
  }
  return (
    <div role="radiogroup" className={styles.segments} aria-labelledby={labelId} aria-describedby={descriptionId} aria-disabled={disabled ? true : undefined}>
      {options.map((option) => (
        <label key={option.value} className={styles.segment}>
          <input type="radio" name={name} checked={option.value === value} disabled={disabled} onChange={() => onChange(option.value)} />
          <span>{option.label}</span>
        </label>
      ))}
    </div>
  );
}
