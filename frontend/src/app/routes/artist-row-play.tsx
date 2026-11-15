import { Play } from "lucide-react";
import { Button } from "../../shared/ui";
import { useArtistPlayback } from "./artist-playback";
import styles from "./artist-row-play.module.css";

/**
 * 艺人列表一行的播放键（由 app 组合进 artists 的列表，artists 不导入 player）：按专辑顺序放这位艺人的全部曲目，替换当前队列。
 * 显隐由列表行负责（悬停或聚焦时盖在圆章上，触屏不显示）。结果只在有新信息时显示，“已选择 N 首”只给读屏。
 */
export function ArtistRowPlay({ artistId, name }: { artistId: string; name: string }) {
  const playback = useArtistPlayback(artistId);
  return (
    <>
      <Button
        variant="icon"
        icon={<Play size={14} fill="currentColor" />}
        label={`播放 ${name} 的全部曲目`}
        disabled={playback.disabledReason !== null}
        busy={playback.pending !== null}
        onClick={() => playback.start("album")}
      />
      <p role="status" className={playback.brief.quiet ? styles.quiet : styles.notice}>
        {playback.brief.text}
      </p>
    </>
  );
}
