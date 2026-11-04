import { Play } from "lucide-react";
import { useState } from "react";
import type { ReleaseDetailDTO } from "../../catalog";
import { Button, FailureNotice } from "../../../shared/ui";
import { playerTracksOf } from "../engine/tracks";
import { unplayableText } from "../engine/capability";
import type { PlayerTrack } from "../engine/queue";
import { usePlaybackCapabilities } from "../hooks/use-playback-capabilities";
import { usePlayerEngine } from "../player-provider";
import styles from "./album-playback-actions.module.css";

function reasonOf(track: PlayerTrack, table: ReturnType<typeof usePlaybackCapabilities>["data"]): string | null {
  if (track.source.sourceKind === "cue_virtual") return "CUE 分轨曲目需要切分，目前不能播放";
  if (table === undefined) return "正在检查播放能力";
  const verdict = table.verdictOf(track.source);
  if (verdict.playable) return null;
  return unplayableText(verdict, track.source.codec);
}

export function AlbumPlaybackActions({ release, groupId }: { release: ReleaseDetailDTO; groupId: string }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const [notice, setNotice] = useState("");
  const tracks = playerTracksOf(release, groupId);
  const playable = capability.data === undefined ? [] : tracks.filter((track) => reasonOf(track, capability.data) === null);
  const skipped = tracks.length - playable.length;
  const disabledReason = capability.isError ? "无法检查播放能力，请重试。" : capability.data === undefined ? "正在检查播放能力…" : playable.length === 0 ? "这个版本没有能播放的曲目。" : null;
  function perform(kind: "play" | "enqueue") {
    const result = kind === "play" ? engine.playTracks(playable) : engine.enqueue(playable);
    if (result === "queue_full") { setNotice("队列已满，请先移除一些条目。"); return; }
    setNotice(`${kind === "play" ? "已选择" : "已加入队列"} ${playable.length} 首${skipped === 0 ? "。" : `，跳过 ${skipped} 首不可播放的曲目。`}`);
  }
  return <div className={styles.container}>
    <div className={styles.actions}><Button variant="primary" icon={<Play size={16} />} disabled={playable.length === 0} onClick={() => perform("play")}>播放整张专辑</Button><Button disabled={playable.length === 0} onClick={() => perform("enqueue")}>加入队列</Button></div>
    {disabledReason === null ? skipped > 0 ? <p>播放或加入队列时会跳过 {skipped} 首不可播放的曲目。</p> : null : <p>{disabledReason}</p>}
    {capability.isError ? <FailureNotice error={capability.error} action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} /> : null}
    <p role="status">{notice}</p>
  </div>;
}
