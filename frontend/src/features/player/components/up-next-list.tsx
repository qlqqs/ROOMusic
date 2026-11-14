import { useId } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { VisuallyHidden } from "../../../shared/ui";
import { upNext } from "../engine/queue";
import { usePlayer } from "../player-provider";
import { PlayerArtwork } from "./player-artwork";
import { failureLabels } from "./player-labels";
import styles from "./up-next-list.module.css";

const UP_NEXT_LIMIT = 20;

/**
 * 全屏播放器右栏的“接下来”：队列里当前条目之后的最多 20 条，点一条就播放它（按条目 ID，同一首多次入队互不影响）。
 * 随机模式下列表不是实际顺序，上方写明；完整的排序与移除在队列抽屉里。
 */
export function UpNextList() {
  const { engine, snapshot } = usePlayer();
  const headingId = useId();
  const entries = upNext(snapshot.queue, UP_NEXT_LIMIT);
  return <section className={styles.panel} aria-labelledby={headingId}>
    <h2 id={headingId} className={styles.heading}><VisuallyHidden>接下来</VisuallyHidden></h2>
    {snapshot.queue.mode === "shuffle" ? <p className={styles.note}>随机播放中，顺序以实际播放为准</p> : null}
    {entries.length === 0 ? <p className={styles.empty}>队列里没有接下来的曲目</p> : <ol role="list" className={styles.list}>
      {entries.map((entry) => {
        const failure = snapshot.failures[entry.entryId];
        return <li key={entry.entryId}>
          <button type="button" className={styles.item} aria-label={`播放《${entry.track.title}》`} onClick={() => engine.playEntry(entry.entryId)}>
            <span className={styles.art} aria-hidden="true"><PlayerArtwork track={entry.track} /></span>
            <span className={styles.text}>
              <span className={styles.title}>{entry.track.title}</span>
              <span className={styles.secondary}>{failure === undefined ? entry.track.artist : failureLabels[failure]}</span>
            </span>
            <span className={styles.duration}>{formatDuration(entry.track.durationSeconds)}</span>
          </button>
        </li>;
      })}
    </ol>}
  </section>;
}
