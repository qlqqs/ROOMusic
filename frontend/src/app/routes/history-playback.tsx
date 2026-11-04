import { Play } from "lucide-react";
import { useState } from "react";
import { formatCount } from "../../shared/lib/number";
import { Button, FailureNotice } from "../../shared/ui";
import { HISTORY_QUEUE_LIMIT, useHistoryQueue, type HistoryItemDTO } from "../../features/listening";
import { usePlaybackCapabilities, usePlayerEngine } from "../../features/player";
import { QUEUE_FULL_TEXT, TrackActions } from "./track-actions";
import { playerTrackOfSummary, unplayableReason } from "./track-playback";
import styles from "./play-all.module.css";

// 历史页的播放动作由 app 组合：listening 不导入 player（player 已导入 listening 的上报器），player 也不认识历史条目。
// 行内动作是各页共用的 TrackActions（播放、心形、“更多”），“从历史中移除”作为这一页自己的菜单项接在后面。

/**
 * 历史页一行的动作：“播放”替换当前队列并只播放这一首（不像专辑页那样从这首起放整张）；
 * “更多”里有“下一首播放”“加入队列”“添加到歌单…”与“从历史中移除”。
 */
export function HistoryTrackActions({ item, onRemove }: { item: HistoryItemDTO; onRemove: () => void }) {
  const track = playerTrackOfSummary(item);
  return (
    <TrackActions
      track={track}
      present={item.present}
      playName={`播放 ${track.title}`}
      retryHint="请在“播放全部”旁重试。"
      extraActions={[{ id: "remove", label: "从历史中移除", onSelect: onRemove }]}
    />
  );
}

/**
 * 历史页顶部的“播放全部”：向服务端读最近最多 1,000 首历史曲目（与列表同一顺序），放入其中可以播放的、替换当前队列并开始播放；
 * 不往更早的历史补位。提示写明放入几首、跳过几首（跳过只算这 1,000 首里不能播放的），历史更长时另说明只处理了最近的 1,000 首。
 * 一首都不能播放时不动当前队列。
 */
export function HistoryPlayAll() {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const queue = useHistoryQueue();
  const [notice, setNotice] = useState("");
  const disabledReason = capability.isError ? "无法检查播放能力，请重试。" : capability.data === undefined ? "正在检查播放能力…" : null;
  function start() {
    const table = capability.data;
    if (table === undefined) return;
    setNotice("");
    queue.load({
      onLoaded: (data) => {
        const tracks = data.items.filter((item) => unplayableReason(playerTrackOfSummary(item), item.present, table) === null).map(playerTrackOfSummary);
        const skipped = data.items.length - tracks.length;
        const scope = data.truncated ? `历史太长，只取了最近的 ${formatCount(HISTORY_QUEUE_LIMIT)} 首。` : "";
        if (tracks.length === 0) {
          setNotice(`${data.items.length === 0 ? "历史里已经没有曲目了" : `这 ${formatCount(data.items.length)} 首都不能播放`}，队列没有变。${scope}`);
          return;
        }
        if (engine.playTracks(tracks) === "queue_full") {
          setNotice(QUEUE_FULL_TEXT);
          return;
        }
        setNotice(`已选择 ${formatCount(tracks.length)} 首${skipped === 0 ? "。" : `，跳过 ${formatCount(skipped)} 首不可播放的曲目。`}${scope}`);
      },
    });
  }
  return (
    <div className={styles.playAll}>
      <div className={styles.actions}>
        <Button variant="primary" icon={<Play size={16} />} disabled={disabledReason !== null} busy={queue.pending} onClick={start}>
          {queue.pending ? "正在读取历史…" : "播放全部"}
        </Button>
      </div>
      {disabledReason === null ? null : <p>{disabledReason}</p>}
      {capability.isError ? <FailureNotice error={capability.error} action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} /> : null}
      {queue.error === null ? null : <FailureNotice error={queue.error} title="无法读取历史" action={<Button onClick={start}>重试</Button>} />}
      <p role="status">{notice}</p>
    </div>
  );
}
