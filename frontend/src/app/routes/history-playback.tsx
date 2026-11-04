import { MoreHorizontal, Play } from "lucide-react";
import { useId, useState } from "react";
import { formatCount } from "../../shared/lib/number";
import { Button, FailureNotice, MenuButton, VisuallyHidden } from "../../shared/ui";
import { HISTORY_QUEUE_LIMIT, historyArtistOf, historyTitleOf, useHistoryQueue, type HistoryItemDTO } from "../../features/listening";
import { unplayableText, usePlaybackCapabilities, usePlayerEngine, type CapabilityTable, type PlayerTrack } from "../../features/player";
import styles from "./history-playback.module.css";

// 历史页的播放动作由 app 组合：listening 不导入 player（player 已导入 listening 的上报器），player 也不认识历史条目。
// 反馈文案沿用专辑页（features/player/components/album-playback-actions.tsx）。

const QUEUE_FULL_TEXT = "队列已满，请先移除一些条目。";

/** 把历史条目转成队列曲目：与发行详情的转换同一套规则（标题空白写“（无标题）”，艺人空白退回专辑艺人）。 */
export function playerTrackOfHistoryItem(item: HistoryItemDTO): PlayerTrack {
  const { release, source } = item;
  return {
    trackId: item.track_id,
    releaseId: release.id,
    groupId: item.group_id,
    title: historyTitleOf(item),
    artist: historyArtistOf(item),
    albumTitle: release.title,
    albumArtist: release.album_artist ?? release.artist,
    durationSeconds: item.duration_seconds,
    artworkId: release.artwork?.resource_id ?? null,
    source: {
      sourceKind: source.source_kind,
      codec: source.codec,
      bitDepth: source.bit_depth,
      sampleRate: source.sample_rate,
      channels: source.channels,
      bitrate: source.bitrate,
    },
  };
}

/** 一首历史曲目为什么不能播放；能播放时是 null。文件缺失优先，其次是浏览器与服务端的能力表。 */
function unplayableReason(item: HistoryItemDTO, table: CapabilityTable | undefined): string | null {
  if (!item.present) return "文件缺失，不能播放";
  if (table === undefined) return "正在检查播放能力";
  const verdict = table.verdictOf({ sourceKind: item.source.source_kind, codec: item.source.codec });
  return verdict.playable ? null : unplayableText(verdict, item.source.codec);
}

/**
 * 历史页一行的播放动作：“播放”替换当前队列并只播放这一首（不像专辑页那样从这首起放整张）；
 * “更多”里有“下一首播放”“加入队列”（不打断当前曲目）与“从历史中移除”。不能播放（文件缺失、格式或浏览器不支持）时前两类不可用，
 * 原因经 aria-describedby 可读；移除始终可用。
 */
export function HistoryTrackActions({ item, onRemove }: { item: HistoryItemDTO; onRemove: () => void }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const [notice, setNotice] = useState("");
  const reasonId = useId();
  const reason = item.present && capability.isError ? "无法检查播放能力，请在“播放全部”旁重试。" : unplayableReason(item, capability.data);
  const title = historyTitleOf(item);
  function play() {
    setNotice(engine.playTracks([playerTrackOfHistoryItem(item)]) === "queue_full" ? QUEUE_FULL_TEXT : "");
  }
  function add(kind: "next" | "enqueue") {
    const track = playerTrackOfHistoryItem(item);
    const result = kind === "next" ? engine.playNext([track]) : engine.enqueue([track]);
    setNotice(result === "queue_full" ? "队列已满，请先移除一些条目。" : kind === "next" ? "已设为下一首播放。" : "已加入队列。");
  }
  return (
    <div className={styles.row}>
      <div className={styles.actions}>
        <Button icon={<Play size={16} />} aria-label={`播放 ${title}`} disabled={reason !== null} aria-describedby={reason === null ? undefined : reasonId} onClick={play}>
          播放
        </Button>
        <MenuButton
          label="更多"
          icon={<MoreHorizontal size={16} />}
          actions={[
            { id: "next", label: "下一首播放", disabled: reason !== null, onSelect: () => add("next") },
            { id: "enqueue", label: "加入队列", disabled: reason !== null, onSelect: () => add("enqueue") },
            { id: "remove", label: "从历史中移除", onSelect: onRemove },
          ]}
        />
      </div>
      {/* 文件缺失已由曲目列的“文件缺失”标签显示，这里只给读屏说明按钮为什么不可用。 */}
      {reason === null ? null : item.present ? <p id={reasonId}>{reason}</p> : <VisuallyHidden><span id={reasonId}>{reason}</span></VisuallyHidden>}
      <p role="status">{notice}</p>
    </div>
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
        const tracks = data.items.filter((item) => unplayableReason(item, table) === null).map(playerTrackOfHistoryItem);
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
