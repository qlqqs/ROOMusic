import { ArrowUp, Square } from "lucide-react";
import { useLayoutEffect, useRef, type KeyboardEvent, type ReactNode } from "react";
import styles from "./composer.module.css";

type ComposerProps = {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  /** 正在回答：发送换成停止。 */
  running: boolean;
  /** 不能发送（正在别处回答、正在创建会话等）；仍可输入。 */
  sendDisabled: boolean;
  onSend: () => void;
  onStop: () => void;
  stopping: boolean;
  /** 输入框下方左侧的工具（模式与模型菜单）。 */
  tools: ReactNode;
  /** 输入框上方的上下文标签行；没有时不渲染。 */
  context?: ReactNode;
  /** 输入框下方的说明（“正在别处回答”等），有它时代替 hint。 */
  note?: ReactNode;
  /** 输入框下方的提示行（随模式变化）；变化时读屏播报。 */
  hint?: ReactNode;
  /** 输入框里按 Shift+Tab：轮换模式（C6）。没有时（不能切换）Shift+Tab 照常移动焦点。 */
  onShiftTab?: (() => void) | undefined;
};

/** 文本框随内容长高，最多这么高，再多就在框里滚动。 */
const MAX_HEIGHT = 200;

/**
 * 输入框：Enter 发送，Shift+Enter 换行，输入法组合期间（选字的回车）不发送；运行中发送按钮变成停止按钮。
 * 焦点在输入框里时 Shift+Tab 轮换模式（只在这里拦截，页面其余地方的 Shift+Tab 照常是反向移动焦点）。
 */
export function Composer({ value, onChange, placeholder, running, sendDisabled, onSend, onStop, stopping, tools, context, note, hint, onShiftTab }: ComposerProps) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    const element = textarea.current;
    if (element === null) return;
    element.style.height = "auto";
    element.style.height = `${Math.min(element.scrollHeight, MAX_HEIGHT)}px`;
  }, [value]);
  const canSend = !running && !sendDisabled && value.trim() !== "";
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Tab" && event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey && onShiftTab !== undefined) {
      event.preventDefault();
      onShiftTab();
      return;
    }
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    if (canSend) onSend();
  };
  return (
    <div className={styles.compose}>
      {context}
      <div className={styles.box}>
        <textarea
          ref={textarea}
          className={styles.input}
          rows={2}
          value={value}
          placeholder={placeholder}
          aria-label={placeholder}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <div className={styles.tools}>
          <div className={styles.pick}>{tools}</div>
          {running ? (
            <button type="button" className={styles.send} data-kind="stop" aria-label="停止回答" aria-busy={stopping ? true : undefined} onClick={() => !stopping && onStop()}>
              <Square size={10} fill="currentColor" aria-hidden="true" />
            </button>
          ) : (
            <button type="button" className={styles.send} aria-label="发送" disabled={!canSend} onClick={onSend}>
              <ArrowUp size={16} aria-hidden="true" />
            </button>
          )}
        </div>
      </div>
      {note !== undefined ? (
        <p className={styles.note}>{note}</p>
      ) : hint === undefined ? null : (
        <p className={styles.note} aria-live="polite">
          {hint}
        </p>
      )}
    </div>
  );
}
