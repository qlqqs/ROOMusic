import { Play } from "lucide-react";
import { useState } from "react";
import { formatCount } from "../../shared/lib/number";
import { Button, FailureNotice } from "../../shared/ui";
import { PERSONAL_TRACK_LIMIT, usePersonalQueue, type PersonalQueueDTO, type PersonalQueueSource } from "../../features/personal";
import { usePlaybackCapabilities, usePlayerEngine, type CapabilityTable } from "../../features/player";
import { QUEUE_FULL_TEXT } from "./track-actions";
import { playerTrackOfSummary, unplayableReason } from "./track-playback";
import styles from "./play-all.module.css";

/**
 * 放进队列的提示：放了几首；跳过了几首（文件缺失的与这个浏览器放不了的分开说）；超出队列上限、这次没放进来的有几首。
 * 跳过与省略是三组不同的数，不重复计算。
 */
export function personalQueueNotice(queued: number, queue: Pick<PersonalQueueDTO, "missing_count" | "omitted_count">, unsupported: number): string {
  const skipped = [
    queue.missing_count > 0 ? `${formatCount(queue.missing_count)} 首文件缺失` : "",
    unsupported > 0 ? `${formatCount(unsupported)} 首不能在这里播放` : "",
  ].filter((part) => part !== "");
  const head = queued === 0 ? "没有能播放的曲目，队列没有变。" : `已选择 ${formatCount(queued)} 首。`;
  const skippedText = skipped.length === 0 ? "" : `跳过了${skipped.join("、")}。`;
  const omitted = queue.omitted_count > 0 ? `另有 ${formatCount(queue.omitted_count)} 首超出 ${formatCount(PERSONAL_TRACK_LIMIT)} 首的上限，这次没有放进来。` : "";
  return `${head}${skippedText}${omitted}`;
}

function playableOf(queue: PersonalQueueDTO, table: CapabilityTable) {
  const tracks = queue.items.map(playerTrackOfSummary).filter((track) => unplayableReason(track, true, table) === null);
  return { tracks, unsupported: queue.items.length - tracks.length };
}

/**
 * 收藏的曲目、或一个歌单的“播放全部”：每次按下都向服务端读一次（先去掉文件缺失的，再按顺序取前 10,000 首），
 * 去掉这个浏览器放不了的，替换当前队列开始播放；原来的队列满了也不影响。一首都不能播放、读取失败或身份变了时，当前队列不变。
 */
export function PersonalPlayAll({ source, label = "播放全部" }: { source: PersonalQueueSource; label?: string }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const queue = usePersonalQueue(source);
  const [notice, setNotice] = useState("");
  // 只是“已选择 N 首”、没有跳过或省略时，结果只给读屏播报，不显示。
  const [quiet, setQuiet] = useState(false);
  const disabledReason = capability.isError ? "无法检查播放能力，请重试。" : capability.data === undefined ? "正在检查播放能力…" : null;
  function start() {
    const table = capability.data;
    if (table === undefined) return;
    setNotice("");
    setQuiet(false);
    queue.load({
      onLoaded: (data) => {
        const { tracks, unsupported } = playableOf(data, table);
        if (tracks.length === 0) {
          setNotice(data.total === 0 ? "还没有曲目，队列没有变。" : personalQueueNotice(0, data, unsupported));
          return;
        }
        if (engine.playTracks(tracks) === "queue_full") {
          setNotice(QUEUE_FULL_TEXT);
          return;
        }
        setNotice(personalQueueNotice(tracks.length, data, unsupported));
        setQuiet(unsupported === 0 && data.missing_count === 0 && data.omitted_count === 0);
      },
    });
  }
  return (
    <div className={styles.playAll}>
      <div className={styles.actions}>
        <Button variant="primary" icon={<Play size={16} />} disabled={disabledReason !== null} busy={queue.pending} onClick={start}>
          {queue.pending ? "正在读取…" : label}
        </Button>
      </div>
      {disabledReason === null ? null : <p>{disabledReason}</p>}
      {capability.isError ? <FailureNotice error={capability.error} action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} /> : null}
      {queue.error === null ? null : <FailureNotice error={queue.error} title="没能读取曲目" action={<Button onClick={start}>重试</Button>} />}
      <p role="status" className={quiet ? styles.quiet : undefined}>{notice}</p>
    </div>
  );
}
