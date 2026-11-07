import { Play, Shuffle } from "lucide-react";
import { useState } from "react";
import { formatCount } from "../../shared/lib/number";
import { newIdempotencyKey } from "../../shared/lib/ids";
import { Button, FailureNotice } from "../../shared/ui";
import { ARTIST_QUEUE_LIMIT, useArtistQueue, type ArtistQueueDTO, type ArtistQueueOrder } from "../../features/artists";
import { usePlaybackCapabilities, usePlayerEngine } from "../../features/player";
import { QUEUE_FULL_TEXT } from "./track-actions";
import { playerTrackOfSummary, unplayableReason } from "./track-playback";
import styles from "./play-all.module.css";

/**
 * 放进队列的提示：放了几首；跳过了几首（文件缺失的与这个浏览器放不了的分开说）；超过 10,000 首时一句话说明这次只放了前 10,000 首。
 */
export function artistQueueNotice(queued: number, queue: Pick<ArtistQueueDTO, "missing_total" | "truncated">, unsupported: number): string {
  const skipped = [
    queue.missing_total > 0 ? `${formatCount(queue.missing_total)} 首文件缺失` : "",
    unsupported > 0 ? `${formatCount(unsupported)} 首不能在这里播放` : "",
  ].filter((part) => part !== "");
  const head = queued === 0 ? "没有能播放的曲目，队列没有变。" : `已选择 ${formatCount(queued)} 首。`;
  const skippedText = skipped.length === 0 ? "" : `跳过了${skipped.join("、")}。`;
  const truncated = queue.truncated ? `曲目较多，本次最多播放 ${formatCount(ARTIST_QUEUE_LIMIT)} 首。` : "";
  return `${head}${skippedText}${truncated}`;
}

/**
 * 艺人页头部的“播放全部”（按专辑顺序）与“随机播放”（在全部曲目里打乱后取前 10,000 首）：每次按下都向服务端读一次，
 * 去掉这个浏览器放不了的，替换当前队列开始播放；不改变播放器的随机与循环设置。一首都不能播放、读取失败或身份变了时当前队列不变。
 */
export function ArtistPlayActions({ artistId }: { artistId: string }) {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const queue = useArtistQueue(artistId);
  const [notice, setNotice] = useState("");
  // 只是“已选择 N 首”、没有跳过或省略时，结果只给读屏播报，不显示。
  const [quiet, setQuiet] = useState(false);
  const [lastOrder, setLastOrder] = useState<ArtistQueueOrder["kind"]>("album");
  const disabledReason = capability.isError ? "无法检查播放能力，请重试。" : capability.data === undefined ? "正在检查播放能力…" : null;
  function start(kind: ArtistQueueOrder["kind"]) {
    const table = capability.data;
    if (table === undefined) return;
    setNotice("");
    setQuiet(false);
    setLastOrder(kind);
    // 随机播放每次换一个 seed：同一个 seed 在数据不变时得到同一个顺序（重试结果一致），换一次按钮就是新的随机。
    const order: ArtistQueueOrder = kind === "random" ? { kind, seed: newIdempotencyKey() } : { kind };
    queue.load(order, (data) => {
      const tracks = data.items.map(playerTrackOfSummary).filter((track) => unplayableReason(track, true, table) === null);
      const unsupported = data.items.length - tracks.length;
      if (tracks.length === 0) {
        setNotice(data.total === 0 && data.missing_total === 0 ? "还没有曲目，队列没有变。" : artistQueueNotice(0, data, unsupported));
        return;
      }
      if (engine.playTracks(tracks) === "queue_full") {
        setNotice(QUEUE_FULL_TEXT);
        return;
      }
      setNotice(artistQueueNotice(tracks.length, data, unsupported));
      setQuiet(unsupported === 0 && data.missing_total === 0 && !data.truncated);
    });
  }
  return (
    <div className={styles.playAll}>
      <div className={styles.actions}>
        <Button variant="primary" icon={<Play size={16} />} disabled={disabledReason !== null} busy={queue.pending === "album"} onClick={() => start("album")}>
          {queue.pending === "album" ? "正在读取…" : "播放全部"}
        </Button>
        <Button icon={<Shuffle size={16} />} disabled={disabledReason !== null} busy={queue.pending === "random"} onClick={() => start("random")}>
          {queue.pending === "random" ? "正在读取…" : "随机播放"}
        </Button>
      </div>
      {disabledReason === null ? null : <p>{disabledReason}</p>}
      {capability.isError ? <FailureNotice error={capability.error} action={<Button onClick={() => void capability.refetch()}>重试检查</Button>} /> : null}
      {queue.error === null ? null : <FailureNotice error={queue.error} title="没能读取曲目" action={<Button onClick={() => start(lastOrder)}>重试</Button>} />}
      <p role="status" className={quiet ? styles.quiet : undefined}>{notice}</p>
    </div>
  );
}
