import { useEffect, useRef, type ReactNode, type RefObject } from "react";
import { useMenu, type MenuAction, type OpenMenuAt } from "./menu-button";

/** 右键不拦截的目标：输入框与可编辑区域（剪贴板菜单）、链接（在新标签页打开等）。 */
function keepsBrowserMenu(target: EventTarget | null, pointer: boolean): boolean {
  if (!(target instanceof Element)) return false;
  if (target.closest("input, textarea, select, [contenteditable='true']") !== null) return true;
  return pointer && target.closest("a[href]") !== null;
}

/** 行里有选中的文字时保留浏览器菜单（复制）。 */
function hasSelectionIn(row: HTMLElement): boolean {
  const selection = window.getSelection();
  return selection !== null && !selection.isCollapsed && selection.anchorNode !== null && row.contains(selection.anchorNode);
}

/**
 * 行的右键菜单：inside 是行里的任一元素，菜单挂到它最近的 [data-context-row] 祖先（调用方给行加这个属性）上。
 * 鼠标右键在指针处弹出；键盘在行内按菜单键或 Shift+F10 时弹在行的左下方。菜单的键盘操作、Esc 与焦点回收沿用 useMenu。
 * 触屏长按、输入框、链接与有选中文字时不拦截，仍是浏览器自己的菜单。
 */
export function useRowContextMenu({ label, actions, inside }: { label: string; actions: readonly MenuAction[]; inside: RefObject<HTMLElement | null> }): ReactNode {
  const menu = useMenu({ label, actions });
  const openAt = useRef<OpenMenuAt>(menu.openAt);
  useEffect(() => {
    openAt.current = menu.openAt;
  });
  useEffect(() => {
    const row = inside.current?.closest<HTMLElement>("[data-context-row]");
    if (row === null || row === undefined) return;
    const atRow = () => {
      const rect = row.getBoundingClientRect();
      openAt.current({ x: rect.left + 24, y: rect.bottom }, row);
    };
    const onContextMenu = (event: MouseEvent) => {
      if ("pointerType" in event && event.pointerType === "touch") return;
      // 键盘触发的 contextmenu（菜单键、Shift+F10）button 不是 2：已在 keydown 里打开，这里只拦下浏览器菜单。
      const pointer = event.button === 2;
      if (keepsBrowserMenu(event.target, pointer) || hasSelectionIn(row)) return;
      event.preventDefault();
      if (pointer) openAt.current({ x: event.clientX, y: event.clientY }, row);
      else atRow();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return;
      if (keepsBrowserMenu(event.target, false)) return;
      event.preventDefault();
      atRow();
    };
    row.addEventListener("contextmenu", onContextMenu);
    row.addEventListener("keydown", onKeyDown);
    return () => {
      row.removeEventListener("contextmenu", onContextMenu);
      row.removeEventListener("keydown", onKeyDown);
    };
  }, [inside]);
  return menu.menu;
}
