import { useEffect, useId, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Button } from "./button";
import styles from "./menu-button.module.css";

export type MenuAction = { id: string; label: string; onSelect: () => void; disabled?: boolean; icon?: ReactNode };

type MenuOptions = {
  /** 菜单的可访问名称。 */
  label: string;
  actions: readonly MenuAction[];
  /** 菜单在触发器下方（默认）还是上方弹出；侧栏底部的按钮用上方。 */
  placement?: "below" | "above";
  /** 菜单与触发器同宽（至少 200px）；默认固定宽度。 */
  matchTriggerWidth?: boolean;
};

type MenuTriggerProps = {
  ref: RefObject<HTMLButtonElement | null>;
  "aria-haspopup": "menu";
  "aria-expanded": boolean;
  "aria-controls": string | undefined;
  onClick: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
};

type Position = { style: CSSProperties; container: Element };

/**
 * 菜单的状态、键盘与浮层；触发器由调用方渲染，把 triggerProps 展开到一个按钮上。
 * 浮层渲染到触发器所在的模态 <dialog> 里（没有时是 body）：模态对话框之外的内容是惰性的，放在 body 上就点不到。
 * 浮在文档层也避免被曲目分段的 content-visibility 裁切。
 */
export function useMenu({ label, actions, placement = "below", matchTriggerWidth = false }: MenuOptions): { triggerProps: MenuTriggerProps; menu: ReactNode; open: boolean } {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<Position | null>(null);
  const first = useRef<"first" | "last">("first");
  const items = () => Array.from(menu.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
  function close(restore = true) {
    setPosition(null);
    if (restore) trigger.current?.focus();
  }
  function open(edge: "first" | "last" = "first") {
    const element = trigger.current;
    if (element === null) return;
    const rect = element.getBoundingClientRect();
    first.current = edge;
    const width = matchTriggerWidth ? Math.min(Math.max(rect.width, 200), window.innerWidth - 16) : 240;
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    const style: CSSProperties =
      placement === "above"
        ? { left, bottom: window.innerHeight - rect.top + 4, maxHeight: Math.max(rect.top - 8, 0) }
        : { left, top: rect.bottom + 4 };
    if (matchTriggerWidth) style.width = width;
    setPosition({ style, container: element.closest("dialog") ?? document.body });
  }
  useEffect(() => {
    if (position === null) return;
    const buttons = items();
    (first.current === "last" ? buttons.at(-1) : buttons[0])?.focus();
    const rect = menu.current?.getBoundingClientRect();
    if (placement === "below" && rect !== undefined && rect.bottom > window.innerHeight - 8) {
      menu.current?.style.setProperty("top", `${Math.max(8, window.innerHeight - rect.height - 8)}px`);
    }
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !menu.current?.contains(event.target) && !trigger.current?.contains(event.target)) setPosition(null);
    };
    const moved = () => setPosition(null);
    document.addEventListener("pointerdown", dismiss);
    window.addEventListener("resize", moved);
    document.addEventListener("scroll", moved, true);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      window.removeEventListener("resize", moved);
      document.removeEventListener("scroll", moved, true);
    };
  }, [position, placement]);
  function onMenuKey(event: KeyboardEvent) {
    const buttons = items();
    const at = buttons.findIndex((button) => button === document.activeElement);
    let next: HTMLButtonElement | undefined;
    if (event.key === "ArrowDown") next = buttons[(at + 1) % buttons.length];
    else if (event.key === "ArrowUp") next = buttons[(at - 1 + buttons.length) % buttons.length];
    else if (event.key === "Home") next = buttons[0];
    else if (event.key === "End") next = buttons.at(-1);
    else if (event.key === "Escape") { event.preventDefault(); close(); return; }
    else if (event.key === "Tab") { close(); return; }
    else return;
    event.preventDefault();
    next?.focus();
  }
  const triggerProps: MenuTriggerProps = {
    ref: trigger,
    "aria-haspopup": "menu",
    "aria-expanded": position !== null,
    "aria-controls": position === null ? undefined : id,
    onClick: () => (position === null ? open() : close()),
    onKeyDown: (event) => {
      if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) { event.preventDefault(); open(event.key === "ArrowUp" ? "last" : "first"); }
    },
  };
  const content = position === null ? null : createPortal(<div id={id} ref={menu} role="menu" aria-label={label} className={styles.menu} style={position.style} onKeyDown={onMenuKey}>
    {actions.map((action) => <button type="button" key={action.id} role="menuitem" tabIndex={-1} disabled={action.disabled} data-icon={action.icon === undefined ? undefined : ""} onClick={() => { close(); action.onSelect(); }}>
      {action.icon === undefined ? null : <span className={styles.icon} aria-hidden="true">{action.icon}</span>}
      {action.label}
    </button>)}
  </div>, position.container);
  return { triggerProps, menu: content, open: position !== null };
}

export function MenuButton({ label, icon, actions, disabled = false, placement }: { label: string; icon?: ReactNode; actions: readonly MenuAction[]; disabled?: boolean; placement?: "below" | "above" }) {
  const menu = useMenu({ label, actions, ...(placement === undefined ? {} : { placement }) });
  return <>
    <Button {...menu.triggerProps} icon={icon} disabled={disabled}>{label}</Button>
    {menu.menu}
  </>;
}
