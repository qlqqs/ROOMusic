import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import type { OperationItemDTO } from "../api/decoders";
import type { ReleaseCommands } from "./use-release-commands";

/**
 * 操作记录列表里“撤销”的开合：点“撤销”打开确认对话框（先清掉上一次的结果提示），关闭时回到列表。
 * 撤销成功之后触发它的那一行变成“已撤销”，“撤销”按钮已经不在页面上，焦点无处可回：此时把焦点带到 fallbackFocus
 * （区块标题或折叠面板的标题行），键盘与读屏用户不会掉回页面顶部。取消、Esc 与失败时按钮仍在，焦点由对话框还给它。
 */
export function useRevertFlow(commands: ReleaseCommands, fallbackFocus: RefObject<HTMLElement | null>) {
  const [target, setTarget] = useState<OperationItemDTO | null>(null);
  const justClosed = useRef(false);
  const { dismiss } = commands;

  const open = useCallback(
    (item: OperationItemDTO) => {
      dismiss();
      setTarget(item);
    },
    [dismiss],
  );
  const close = useCallback(() => {
    justClosed.current = true;
    setTarget(null);
  }, []);

  // 对话框的清理（还焦点给打开前的元素）先于这里的副作用执行：此时焦点仍在 body，说明没有地方可回。
  useEffect(() => {
    if (target !== null || !justClosed.current) return;
    justClosed.current = false;
    if (document.activeElement === null || document.activeElement === document.body) fallbackFocus.current?.focus();
  }, [target, fallbackFocus]);

  return { target, open, close };
}
