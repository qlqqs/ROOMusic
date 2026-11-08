import { ListPlus } from "lucide-react";
import { useMemo, useState } from "react";
import { formatCount } from "../shared/lib/number";
import { Button } from "../shared/ui";
import { CreatePlaylistDialog, FavoriteButton, FavoriteStatusScope, queuePlaylistName } from "../features/personal";
import type { PlayerTrack } from "../features/player";
import styles from "./player-personal-actions.module.css";

// 播放器上的收藏与歌单入口：player 只提供中立的插槽（当前曲目、队列里的曲目），这里把 personal 的组件放进去。
// player 不导入 personal，也不从引擎调用个人数据接口。

/** 当前曲目的心形（播放器栏、全屏播放器）：切曲只换目标，状态按曲目身份读取。 */
export function CurrentTrackFavorite({ track, message = "visible" }: { track: PlayerTrack; message?: "visible" | "screen-reader" }) {
  const trackIds = useMemo(() => [track.trackId], [track.trackId]);
  return (
    <FavoriteStatusScope trackIds={trackIds}>
      <FavoriteButton target={{ kind: "track", id: track.trackId, title: track.title }} message={message} />
    </FavoriteStatusScope>
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
