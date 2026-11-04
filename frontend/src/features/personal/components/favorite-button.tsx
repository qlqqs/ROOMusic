import { Heart } from "lucide-react";
import { useEffect, useEffectEvent } from "react";
import { Button, VisuallyHidden } from "../../../shared/ui";
import type { FavoriteKind } from "../api/decoders";
import { favoriteFailureText, useFavoriteToggle } from "../hooks/use-favorite-toggle";
import { useFavoriteStatus } from "./favorite-status-scope";
import styles from "./favorite-button.module.css";

export type FavoriteTarget = { kind: FavoriteKind; id: string; title: string };

type FavoriteButtonProps = {
  target: FavoriteTarget;
  /**
   * 切换失败的那句话放在哪里：默认显示在心形旁边；“screen-reader”只给读屏（播放器栏放不下）；
   * 传入函数时交给调用方显示（曲目行把它和播放的提示放在同一行），开始新的切换时先收到空字符串。
   */
  message?: "visible" | "screen-reader" | ((text: string) => void);
};

/**
 * 心形：切换按钮（aria-pressed），读屏名称是“收藏《标题》”，已收藏为实心。状态来自所在的 FavoriteStatusScope；
 * 点下去先按新状态显示，失败时回到原状态并说一句。同一目标的切换还没结束时，再点不起作用。
 * 结果不确定（写请求可能已经执行）时，所有位置的这个目标都保持忙碌，直到重新读到确定的状态；重读也失败时仍然不能切换，
 * 旁边说一句并给“重新读取”。
 */
export function FavoriteButton({ target, message = "visible" }: FavoriteButtonProps) {
  const status = useFavoriteStatus();
  const { toggle, failure } = useFavoriteToggle(target.kind, target.id);
  const pressed = status.statusOf(target.kind, target.id);
  const unconfirmed = status.unconfirmedOf(target.kind, target.id);
  const busy = status.pendingOf(target.kind, target.id) || unconfirmed;
  const waiting = unconfirmed && !status.refreshing;
  // 结果不确定时的那句话随重读的进展变化：重读中（只有发起切换的位置说）、重读失败（每个位置都说）、确认之后清掉。
  // null 表示与结果不确定无关，由普通的失败文案决定。
  const uncertainText = unconfirmed ? (waiting ? "没能确认是否已收藏。" : failure?.uncertain === true ? favoriteFailureText(failure) : "") : failure?.uncertain === true ? "" : null;
  const text = uncertainText ?? (failure === null ? "" : favoriteFailureText(failure));
  const hint = pressed === undefined ? (status.failed ? "暂时读不到收藏状态" : "正在读取收藏状态") : pressed ? "取消收藏" : "收藏";
  const report = typeof message === "function" ? message : undefined;
  const reportUncertain = useEffectEvent((value: string) => report?.(value));
  useEffect(() => {
    if (uncertainText !== null) reportUncertain(uncertainText);
  }, [uncertainText]);
  const button = (
    <Button
      variant="text"
      icon={<Heart size={18} fill={pressed === true ? "currentColor" : "none"} />}
      aria-label={`收藏《${target.title}》`}
      aria-pressed={pressed ?? false}
      title={hint}
      disabled={pressed === undefined}
      busy={busy}
      onClick={() => {
        if (pressed === undefined) return;
        report?.("");
        toggle(!pressed, { onFailed: (result) => report?.(favoriteFailureText(result)) });
      }}
    />
  );
  const retry = waiting ? (
    <Button variant="text" aria-label={`重新读取《${target.title}》的收藏状态`} onClick={status.reread}>
      重新读取
    </Button>
  ) : null;
  if (report !== undefined) {
    return (
      <span className={styles.favorite}>
        {button}
        {retry}
      </span>
    );
  }
  return (
    <span className={styles.favorite}>
      {button}
      {message === "visible" ? (
        <span role="status" className={styles.message}>
          {text}
        </span>
      ) : (
        <VisuallyHidden>
          <span role="status">{text}</span>
        </VisuallyHidden>
      )}
      {retry}
    </span>
  );
}
