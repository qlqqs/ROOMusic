import { Play } from "lucide-react";
import { useState } from "react";
import { useReleaseLoader, type ReleaseGroupSummaryDTO } from "../../features/catalog";
import { playerTracksOf, usePlaybackCapabilities, usePlayerEngine } from "../../features/player";
import { formatCount } from "../../shared/lib/number";
import { Button } from "../../shared/ui";
import { QUEUE_FULL_TEXT } from "./track-actions";
import { unplayableReason } from "./track-playback";
import styles from "./album-card-play.module.css";

/**
 * 专辑卡片封面上的圆形“播放”（由 app 组合进 catalog 的卡片，catalog 不导入 player）：读代表版本的详情（与详情页共用缓存），
 * 放整张里能播放的曲目，替换当前队列。显隐由卡片负责（悬停或聚焦时出现，触屏不显示）。
 * 结果只在有新信息时显示在封面上（没有能播放的曲目、队列已满、读取失败），“已选择 N 首”只给读屏。
 */
export function AlbumCardPlay({ group }: { group: ReleaseGroupSummaryDTO }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const loadRelease = useReleaseLoader();
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState({ text: "", quiet: false });
  const release = group.representative;
  async function play() {
    const table = capability.data;
    if (table === undefined) return;
    setPending(true);
    setNotice({ text: "", quiet: false });
    try {
      const detail = await loadRelease(release.id);
      const tracks = playerTracksOf(detail, group.id).filter((track) => unplayableReason(track, true, table) === null);
      if (tracks.length === 0) setNotice({ text: "这张专辑没有能播放的曲目。", quiet: false });
      else if (engine.playTracks(tracks) === "queue_full") setNotice({ text: QUEUE_FULL_TEXT, quiet: false });
      else setNotice({ text: `已选择 ${formatCount(tracks.length)} 首。`, quiet: true });
    } catch {
      // 401 由请求层结束会话；其余失败就地说一句，再按一次就是重试。
      setNotice({ text: "没能读取这张专辑，请重试。", quiet: false });
    } finally {
      setPending(false);
    }
  }
  return (
    <>
      <Button variant="icon" icon={<Play size={18} fill="currentColor" />} label={`播放《${release.title}》`} disabled={capability.data === undefined} busy={pending} onClick={() => void play()} />
      <p role="status" className={notice.quiet ? styles.quiet : styles.notice}>
        {notice.text}
      </p>
    </>
  );
}
