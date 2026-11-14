import { ListEnd, ListStart, Play, Shuffle } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { ReleaseDetailDTO } from "../../catalog";
import { Button, FailureNotice, type MenuAction } from "../../../shared/ui";
import { playerTracksOf } from "../engine/tracks";
import { unplayableText } from "../engine/capability";
import { MAX_QUEUE_ENTRIES, type PlayerTrack } from "../engine/queue";
import { usePlaybackCapabilities } from "../hooks/use-playback-capabilities";
import { usePlayerEngine } from "../player-provider";
import styles from "./album-playback-actions.module.css";

function reasonOf(track: PlayerTrack, table: ReturnType<typeof usePlaybackCapabilities>["data"]): string | null {
  if (table === undefined) return "正在检查播放能力";
  const verdict = table.verdictOf(track.source);
  if (verdict.playable) return null;
  return unplayableText(verdict);
}

type AlbumPlayback = {
  /** 墨色圆形的“播放整张专辑”：操作行里唯一的主要操作。 */
  play: ReactNode;
  /** “随机播放”：切到随机模式，从随机的一首开始放整张。 */
  shuffle: ReactNode;
  /** 放进“更多”菜单的项：下一首播放（整张插到当前曲目之后）、加入队列。 */
  actions: MenuAction[];
  /** 不能播放的原因、跳过的曲目、能力检查失败与上一次操作的结果；放在操作行下方，没有内容时不占位。 */
  status: ReactNode;
};

type Kind = "play" | "shuffle" | "next" | "enqueue";

const doneText: Readonly<Record<Kind, string>> = { play: "已选择", shuffle: "已随机播放", next: "已设为下一首播放", enqueue: "已加入队列" };

/**
 * 专辑头部的播放动作：页面（app）把它们与收藏、“添加到歌单…”、管理员的项组合成一条操作行与一个“更多”菜单。
 * 四种动作都只放这个版本里能播放的曲目，跳过的曲目数写在结果里；队列放不下时整批不放并说明。
 */
export function useAlbumPlayback({ release, groupId }: { release: ReleaseDetailDTO; groupId: string }): AlbumPlayback {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  // quiet：只是“已选择 N 首”这类没有新信息的结果，只给读屏播报，不显示。
  const [notice, setNotice] = useState({ text: "", quiet: false });
  const tracks = playerTracksOf(release, groupId);
  const playable = capability.data === undefined ? [] : tracks.filter((track) => reasonOf(track, capability.data) === null);
  const skipped = tracks.length - playable.length;
  const unavailable = playable.length === 0;
  const disabledReason = capability.isError ? "无法检查播放能力，请重试。" : capability.data === undefined ? "正在检查播放能力…" : unavailable ? "这个版本没有能播放的曲目。" : null;
  function perform(kind: Kind) {
    let result;
    if (kind === "shuffle") {
      // 先确认放得下再切模式：放不下时模式也不变。随机起点：playTracks 默认从第一首开始，随机播放每次都从第一首放起就不像随机了。
      if (playable.length > MAX_QUEUE_ENTRIES) result = "queue_full" as const;
      else {
        engine.setMode("shuffle");
        result = engine.playTracks(playable, Math.floor(Math.random() * playable.length));
      }
    } else if (kind === "play") result = engine.playTracks(playable);
    else if (kind === "next") result = engine.playNext(playable);
    else result = engine.enqueue(playable);
    if (result === "queue_full") {
      setNotice({ text: "队列已满，请先移除一些条目。", quiet: false });
      return;
    }
    setNotice({ text: `${doneText[kind]} ${playable.length} 首${skipped === 0 ? "。" : `，跳过 ${skipped} 首不可播放的曲目。`}`, quiet: (kind === "play" || kind === "shuffle") && skipped === 0 });
  }
  return {
    play: (
      <span className={styles.play}>
        <Button variant="icon" icon={<Play size={20} fill="currentColor" />} label="播放整张专辑" disabled={unavailable} onClick={() => perform("play")} />
      </span>
    ),
    shuffle: (
      <Button icon={<Shuffle size={16} />} disabled={unavailable} onClick={() => perform("shuffle")}>
        随机播放
      </Button>
    ),
    actions: [
      { id: "play-next", label: "下一首播放", icon: <ListStart size={16} />, disabled: unavailable, onSelect: () => perform("next") },
      { id: "enqueue", label: "加入队列", icon: <ListEnd size={16} />, disabled: unavailable, onSelect: () => perform("enqueue") },
    ],
    status: (
      <div className={styles.status}>
        {disabledReason === null ? skipped > 0 ? <p>播放或加入队列时会跳过 {skipped} 首不可播放的曲目。</p> : null : <p>{disabledReason}</p>}
        {capability.isError ? <FailureNotice error={capability.error} action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} /> : null}
        <p role="status" className={notice.quiet ? styles.quiet : undefined}>
          {notice.text}
        </p>
      </div>
    ),
  };
}
