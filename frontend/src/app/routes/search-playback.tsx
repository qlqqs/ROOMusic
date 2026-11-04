import { MoreHorizontal, Play } from "lucide-react";
import { useId, useState } from "react";
import { trackTitleOf } from "../../features/catalog";
import type { SearchTrackItemDTO } from "../../features/search";
import { usePlaybackCapabilities, usePlayerEngine } from "../../features/player";
import { Button, FailureNotice, MenuButton, VisuallyHidden } from "../../shared/ui";
import { QUEUE_FULL_TEXT } from "./track-actions";
import { playerTrackOfSummary, unplayableReason } from "./track-playback";
import styles from "./track-actions.module.css";

// 搜索结果曲目行的动作由 app 组合：search 不导入 player，player 也不认识搜索结果。曲目摘要转队列曲目、能否播放的判断与其他页共用（track-playback.ts）。
// 之后的心形与“添加到歌单”改用统一的 TrackActions（track-actions.tsx），替换这里的组件即可，结果页只认 renderTrackActions 插槽。

/**
 * 一行的动作：“播放”替换当前队列并只播放这一首；“更多”里有“下一首播放”“加入队列”（不打断当前曲目）。
 * 不能播放（文件缺失、格式或浏览器不支持）时都不可用，原因经 aria-describedby 可读。
 */
export function SearchTrackActions({ item }: { item: SearchTrackItemDTO }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const [notice, setNotice] = useState("");
  const reasonId = useId();
  const track = playerTrackOfSummary(item);
  const reason = item.present && capability.isError ? "无法检查播放能力。" : unplayableReason(track, item.present, capability.data);
  const title = trackTitleOf(item);
  function play() {
    setNotice(engine.playTracks([track]) === "queue_full" ? QUEUE_FULL_TEXT : "");
  }
  function add(kind: "next" | "enqueue") {
    const result = kind === "next" ? engine.playNext([track]) : engine.enqueue([track]);
    setNotice(result === "queue_full" ? QUEUE_FULL_TEXT : kind === "next" ? "已设为下一首播放。" : "已加入队列。");
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
          ]}
        />
      </div>
      {reason === null ? null : item.present ? <p id={reasonId}>{reason}</p> : <VisuallyHidden><span id={reasonId}>{reason}</span></VisuallyHidden>}
      <p role="status">{notice}</p>
    </div>
  );
}

/** 曲目栏上方：无法检查播放能力时给一次重试（不在每一行重复）；能检查时不渲染。 */
export function SearchPlaybackNotice() {
  const capability = usePlaybackCapabilities();
  if (!capability.isError) return null;
  return <FailureNotice error={capability.error} title="无法检查播放能力" action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} />;
}
