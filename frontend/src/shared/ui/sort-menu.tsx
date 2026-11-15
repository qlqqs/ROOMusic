import { ArrowUpDown, Check, ChevronDown } from "lucide-react";
import { Button } from "./button";
import { useMenu, type MenuAction } from "./menu-button";
import styles from "./sort-menu.module.css";

export type SortOption<T extends string> = { value: T; label: string };

type SortMenuProps<T extends string> = {
  options: readonly SortOption<T>[];
  value: T;
  onChange: (value: T) => void;
};

/**
 * 列表页工具条右端的排序（专辑、艺人共用）：按钮写着当前的排序方式，点开是一个菜单，当前项带勾与“当前”。
 * 按钮的可访问名称是“排序：按标题”，菜单名是“排序”。
 */
export function SortMenu<T extends string>({ options, value, onChange }: SortMenuProps<T>) {
  const current = options.find((option) => option.value === value) ?? options[0];
  const actions: MenuAction[] = options.map((option) => ({
    id: option.value,
    label: option.label,
    icon: option.value === value ? <Check size={16} /> : <span className={styles.blank} />,
    ...(option.value === value ? { hint: "当前" } : {}),
    onSelect: () => {
      if (option.value !== value) onChange(option.value);
    },
  }));
  const menu = useMenu({ label: "排序", actions });
  return (
    <>
      <Button {...menu.triggerProps} icon={<ArrowUpDown size={16} />} aria-label={`排序：${current?.label ?? ""}`}>
        <span className={styles.trigger}>
          {current?.label}
          <ChevronDown size={14} aria-hidden="true" />
        </span>
      </Button>
      {menu.menu}
    </>
  );
}
