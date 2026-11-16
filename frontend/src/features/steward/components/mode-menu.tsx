import { ChevronDown, CircleAlert, ShieldCheck, Zap, type LucideIcon } from "lucide-react";
import { useMenu, type MenuAction } from "../../../shared/ui";
import type { StewardMode } from "../api/decoders";
import styles from "./thread.module.css";

// 模式（C6）：输入框下的按钮显示当前模式，点开是菜单，输入框里按 Shift+Tab 轮换。本任务只有 Assistant 与 Operator（决定 D3），
// Steward 随 C7 加进菜单；会话已是 steward 模式时按钮照样显示它的名字。

type ModeInfo = { name: string; icon: LucideIcon; description: string; hint: string };

export const modeInfo: Readonly<Record<StewardMode, ModeInfo>> = {
  assistant: { name: "Assistant", icon: CircleAlert, description: "修改曲库前先给你看方案，你确认后执行", hint: "修改曲库前会先给你看方案" },
  steward: { name: "Steward", icon: ShieldCheck, description: "由另一个模型审查，通过的自动执行", hint: "修改曲库前由另一个模型审查" },
  operator: { name: "Operator", icon: Zap, description: "不询问，直接执行；外部资料里的内容也可能促成修改", hint: "修改曲库时不询问，直接执行" },
};

/** 菜单里可选的模式，也是 Shift+Tab 轮换的顺序。 */
export const selectableModes: readonly StewardMode[] = ["assistant", "operator"];

/** Shift+Tab 的下一个模式。 */
export function nextMode(current: StewardMode): StewardMode {
  const index = selectableModes.indexOf(current);
  return selectableModes[(index + 1) % selectableModes.length] ?? "assistant";
}

type ModeMenuProps = {
  current: StewardMode;
  /** 正在回答、有未处理的方案或正在切换时不能改。 */
  disabled: boolean;
  onSelect: (mode: StewardMode) => void;
};

export function ModeMenu({ current, disabled, onSelect }: ModeMenuProps) {
  const info = modeInfo[current];
  const actions: MenuAction[] = selectableModes.map((mode) => {
    const { name, icon: Icon, description } = modeInfo[mode];
    return {
      id: `mode:${mode}`,
      label: name,
      description,
      icon: <Icon size={14} />,
      // 勾号只是图形；“当前”写在项名里，读屏也能知道。
      ...(mode === current ? { hint: "当前" } : {}),
      onSelect: () => {
        if (mode !== current) onSelect(mode);
      },
    };
  });
  const menu = useMenu({
    label: "模式",
    actions,
    placement: "above",
    footer: (
      <>
        <kbd>Shift</kbd> + <kbd>Tab</kbd> 在输入框里切换
      </>
    ),
  });
  const Icon = info.icon;
  return (
    <>
      <button type="button" className={styles.modeButton} {...menu.triggerProps} disabled={disabled} aria-label={`模式：${info.name}`}>
        <Icon size={14} aria-hidden="true" />
        <span>{info.name}</span>
        <ChevronDown size={14} aria-hidden="true" className={styles.modeChevron} />
      </button>
      {menu.menu}
    </>
  );
}
