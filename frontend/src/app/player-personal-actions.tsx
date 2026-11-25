import { ListPlus, Star } from "lucide-react";
import { useMemo, useState } from "react";
import { formatCount } from "../shared/lib/number";
import { Button } from "../shared/ui";
import {
  CreatePlaylistDialog,
  FavoriteButton,
  FavoriteStatusScope,
  queuePlaylistName,
  RatingControl,
  RatingStatusScope,
  useRatingPopover,
  useRatingStatus,
  type RatingTarget,
} from "../features/personal";
import type { PlayerTrack } from "../features/player";
import styles from "./player-personal-actions.module.css";

// 播放器上的收藏、评分与歌单入口：player 只提供中立的插槽（当前曲目、队列里的曲目），这里把 personal 的组件放进去。
// player 不导入 personal，也不从播放器调用个人数据接口。

/**
 * 当前曲目的心形与星级（播放器栏、全屏播放器）：切曲只换目标，状态按曲目身份读取。
 * 播放器栏放不下五颗星：心形右边一颗星，评过分时实心并带数字，点开评分浮层；全屏播放器直接放五颗星。
 */
export function CurrentTrackPersonal({ track, variant }: { track: PlayerTrack; variant: "bar" | "fullscreen" }) {
  const trackIds = useMemo(() => [track.trackId], [track.trackId]);
  const target: RatingTarget = { kind: "track", id: track.trackId, title: track.title };
  return (
    <FavoriteStatusScope trackIds={trackIds}>
      <RatingStatusScope trackIds={trackIds}>
        <span className={styles.current}>
          <FavoriteButton target={target} message={variant === "bar" ? "screen-reader" : "visible"} />
          {variant === "bar" ? <BarRating key={track.trackId} target={target} /> : <RatingControl target={target} />}
        </span>
      </RatingStatusScope>
    </FavoriteStatusScope>
  );
}

/** 播放器栏的星：读屏名“评分《标题》：3 星”，点开评分浮层（浮在播放器栏上方）。 */
function BarRating({ target }: { target: RatingTarget }) {
  const status = useRatingStatus();
  const rating = status.ratingOf(target.kind, target.id);
  const { open, isOpen, popover } = useRatingPopover(target);
  const value = rating === undefined ? (status.failed ? "暂时读不到评分" : "正在读取评分") : rating === 0 ? "未评分" : `${rating} 星`;
  return (
    <span className={styles.barRating} data-rated={rating !== undefined && rating > 0 ? "" : undefined}>
      <Button
        variant="icon"
        icon={<Star size={16} fill={rating !== undefined && rating > 0 ? "currentColor" : "none"} />}
        label={`评分《${target.title}》：${value}`}
        tooltip={rating === undefined || rating === 0 ? "评分" : value}
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        disabled={rating === undefined}
        onClick={(event) => open(event.currentTarget, "above")}
      />
      {rating !== undefined && rating > 0 ? (
        <span className={styles.count} aria-hidden="true">
          {rating}
        </span>
      ) : null}
      {popover}
    </span>
  );
}

/** 去掉队列里重复的曲目，只留第一次出现的，保持队列顺序。 */
export function distinctTrackIds(tracks: readonly PlayerTrack[]): string[] {
  return [...new Set(tracks.map((track) => track.trackId))];
}

/** 队列的“存为歌单”：按此刻的队列顺序（重复的只留第一次）新建一个歌单，名字预填“队列 年-月-日”。 */
export function SaveQueueAsPlaylist({ tracks }: { tracks: readonly PlayerTrack[] }) {
  const [snapshot, setSnapshot] = useState<{ trackIds: string[]; name: string } | null>(null);
  const [notice, setNotice] = useState("");
  return (
    <span className={styles.save}>
      <Button
        variant="icon"
        icon={<ListPlus size={16} />}
        label="存为歌单"
        onClick={() => {
          setNotice("");
          setSnapshot({ trackIds: distinctTrackIds(tracks), name: queuePlaylistName() });
        }}
      />
      <span role="status" className={styles.notice}>
        {notice}
      </span>
      <CreatePlaylistDialog
        open={snapshot !== null}
        title="存为歌单"
        submitLabel="存为歌单"
        initialName={snapshot?.name ?? ""}
        trackIds={snapshot?.trackIds ?? []}
        onClose={() => setSnapshot(null)}
        onCreated={(result) => {
          setSnapshot(null);
          setNotice(`已存为《${result.playlist.name}》，共 ${formatCount(result.playlist.track_count)} 首。`);
        }}
      />
    </span>
  );
}
