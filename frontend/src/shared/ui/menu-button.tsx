import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "./button";
import styles from "./menu-button.module.css";

export type MenuAction = { id: string; label: string; onSelect: () => void; disabled?: boolean };

/** 菜单浮在文档层，避免被曲目分段的 content-visibility 裁切。 */
export function MenuButton({ label, icon, actions, disabled = false }: { label: string; icon?: ReactNode; actions: readonly MenuAction[]; disabled?: boolean }) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null);
  const first = useRef<"first" | "last">("first");
  const items = () => Array.from(menu.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
  function close(restore = true) {
    setPosition(null);
    if (restore) trigger.current?.focus();
  }
  function open(edge: "first" | "last" = "first") {
    const rect = trigger.current?.getBoundingClientRect();
    if (rect === undefined) return;
    first.current = edge;
    setPosition({ left: Math.max(8, Math.min(rect.left, window.innerWidth - 248)), top: rect.bottom + 4 });
  }
  useEffect(() => {
    if (position === null) return;
    const buttons = items();
    (first.current === "last" ? buttons.at(-1) : buttons[0])?.focus();
    const rect = menu.current?.getBoundingClientRect();
    if (rect !== undefined && rect.bottom > window.innerHeight - 8) {
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
  }, [position]);
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
  return <>
    <Button ref={trigger} icon={icon} disabled={disabled} aria-haspopup="menu" aria-expanded={position !== null} aria-controls={position === null ? undefined : id}
      onClick={() => position === null ? open() : close()}
      onKeyDown={(event) => {
        if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) { event.preventDefault(); open(event.key === "ArrowUp" ? "last" : "first"); }
      }}>{label}</Button>
    {position === null ? null : createPortal(<div id={id} ref={menu} role="menu" aria-label={label} className={styles.menu} style={position} onKeyDown={onMenuKey}>
      {actions.map((action) => <button type="button" key={action.id} role="menuitem" tabIndex={-1} disabled={action.disabled} onClick={() => { close(); action.onSelect(); }}>{action.label}</button>)}
    </div>, document.body)}
  </>;
}
