import { useState } from "react";
import { cx } from "../../../shared/lib/class-names";
import { VisuallyHidden } from "../../../shared/ui";
import type { FavoriteKind } from "../api/decoders";
import { ratingFailureText, useRatingWrite } from "../hooks/use-rating";
import { RatingStars } from "./rating-stars";
import { useRatingStatus } from "./rating-status-scope";
import styles from "./rating-control.module.css";

export type RatingTarget = { kind: FavoriteKind; id: string; title: string };

/** 读屏名里的对象：作品名用书名号，艺人用引号（同心形）。 */
export function ratingSubject(target: RatingTarget): string {
  return target.kind === "artist" ? `“${target.title}”` : `《${target.title}》`;
}

/** 还没读到评分时读屏与提示说什么。 */
export function unavailableRatingText(failed: boolean): string {
  return failed ? "暂时读不到评分" : "正在读取评分";
}

type RatingControlProps = {
  target: RatingTarget;
  size?: "sm" | "md" | "lg";
  /** 写入失败的那句话：默认显示在星级旁边；“screen-reader”只给读屏。 */
  message?: "visible" | "screen-reader";
  /** 头部用：窄屏时星级排到操作行下面单独一行，宽屏时与心形之间一条细线。 */
  placement?: "inline" | "header";
  autoFocus?: boolean;
  /** 写入成功之后（浮层据此关闭）。 */
  onSaved?: () => void;
};

/**
 * 接上本人评分的星级：值来自所在的 RatingStatusScope，写入时先按新值显示，失败时回到原值并说一句。
 * 专辑与艺人详情的头部、全屏播放器与评分浮层都用它。
 */
export function RatingControl({ target, size = "md", message = "visible", placement = "inline", autoFocus = false, onSaved }: RatingControlProps) {
  const status = useRatingStatus();
  const { write } = useRatingWrite(target.kind, target.id);
  const [text, setText] = useState("");
  const value = status.ratingOf(target.kind, target.id);
  const notice = <span role="status" className={styles.message}>{text}</span>;
  return (
    <span className={cx(styles.control, placement === "header" && styles.header)}>
      <RatingStars
        label={`评分${ratingSubject(target)}`}
        value={value}
        unavailableText={unavailableRatingText(status.failed)}
        busy={status.pendingOf(target.kind, target.id)}
        size={size}
        autoFocus={autoFocus}
        onCommit={(rating) => {
          setText("");
          write(rating, { onSucceeded: onSaved, onFailed: (failure) => setText(ratingFailureText(failure)) });
        }}
      />
      {message === "visible" ? notice : <VisuallyHidden>{notice}</VisuallyHidden>}
    </span>
  );
}
