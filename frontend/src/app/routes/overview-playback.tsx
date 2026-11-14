import { Link } from "@tanstack/react-router";
import { Disc3, Pause, Play } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { albumArtistOf, artworkPath, useReleaseLoader } from "../../features/catalog";
import { useRecentReleases, type RecentReleaseDTO } from "../../features/listening";
import { playerTracksOf, usePlaybackCapabilities, usePlayerEngine, usePlayerProgress, usePlayerSelector, type PlayerTrack } from "../../features/player";
import { formatDuration } from "../../shared/lib/duration";
import { formatRelativeTime } from "../../shared/lib/time";
import { Button, FailureNotice } from "../../shared/ui";
import { QUEUE_FULL_TEXT } from "./track-actions";
import { unplayableReason } from "./track-playback";
import styles from "./overview-playback.module.css";

// 概览里的播放入口由 app 组合：listening 给出最近播放，player 负责播放，二者互不导入。

type StartTarget = { groupId: string; releaseId: string; title: string; trackId: string | null };

type StartResult = { kind: "text"; text: string } | { kind: "failed"; error: unknown; target: StartTarget };

/**
 * 从最近播放的一张专辑开始播放：读这个版本的详情（与详情页共用缓存），放入其中能播放的曲目、替换当前队列；
 * 给了 trackId 时从那一首开始（那首现在不能播放时从第一首开始）。结果只在失败、队列已满或一首都不能播放时有文字。
 */
function useReleaseStarter() {
  const engine = usePlayerEngine();
  const capability = usePlaybackCapabilities();
  const loadRelease = useReleaseLoader();
  const [pending, setPending] = useState<string | null>(null);
  const [result, setResult] = useState<StartResult | null>(null);
  async function start(target: StartTarget) {
    const table = capability.data;
    if (table === undefined || pending !== null) return;
    setPending(target.releaseId);
    setResult(null);
    try {
      const release = await loadRelease(target.releaseId);
      const playable = playerTracksOf(release, target.groupId).filter((track) => unplayableReason(track, true, table) === null);
      if (playable.length === 0) {
        setResult({ kind: "text", text: `《${target.title}》没有能播放的曲目。` });
        return;
      }
      const at = target.trackId === null ? 0 : Math.max(0, playable.findIndex((track) => track.trackId === target.trackId));
      setResult(engine.playTracks(playable, at) === "queue_full" ? { kind: "text", text: QUEUE_FULL_TEXT } : null);
    } catch (error) {
      setResult({ kind: "failed", error, target });
    } finally {
      setPending(null);
    }
  }
  const status =
    result === null ? null : result.kind === "text" ? (
      <p role="status" className={styles.status}>
        {result.text}
      </p>
    ) : (
      <FailureNotice error={result.error} title="无法开始播放" action={<Button onClick={() => void start(result.target)}>重试</Button>} />
    );
  return { start, pending, ready: capability.data !== undefined, status };
}

/** “最近播放”的卡片操作：每张卡片封面右下角的播放键，从上次播放的那一首开始放这张专辑；结果统一显示在横排下方。 */
export function useRecentCardPlayback(): { renderCardAction: (item: RecentReleaseDTO) => ReactNode; status: ReactNode } {
  const starter = useReleaseStarter();
  return {
    renderCardAction: (item) => (
      <span className={styles.cardPlay}>
        <Button
          variant="icon"
          icon={<Play size={18} fill="currentColor" />}
          label={`播放《${item.release.title}》`}
          disabled={!starter.ready}
          busy={starter.pending === item.release.id}
          onClick={() => void starter.start({ groupId: item.group_id, releaseId: item.release.id, title: item.release.title, trackId: item.last_track.id })}
        />
      </span>
    ),
    status: starter.status,
  };
}

/** 封面：同源封面接口的原图，没有或加载失败时是同尺寸的唱片标记。装饰性，专辑名在旁边的文字里。 */
function ResumeArtwork({ artworkId }: { artworkId: string | null }) {
  const [failedId, setFailedId] = useState<string | null>(null);
  return (
    <span className={styles.art} aria-hidden="true">
      {artworkId === null || failedId === artworkId ? <Disc3 size={40} /> : <img src={artworkPath(artworkId)} alt="" onError={() => setFailedId(artworkId)} />}
    </span>
  );
}

/** 三根音柱：播放时跳动，减少动态效果时静止。 */
function Bars({ playing }: { playing: boolean }) {
  return (
    <span className={styles.eq} data-playing={playing ? "" : undefined} aria-hidden="true">
      <i />
      <i />
      <i />
    </span>
  );
}

/** 当前曲目的进度文字与细进度条：只订阅进度，取自播放器的真实事件；时长未知时只写已播放的时间。 */
function CurrentProgress() {
  const progress = usePlayerProgress();
  const duration = progress.durationSeconds;
  const percent = duration === null || duration <= 0 ? null : Math.min(100, (progress.positionSeconds / duration) * 100);
  return (
    <span className={styles.progress} aria-hidden="true">
      <span className={styles.track}>{percent === null ? null : <span className={styles.fill} style={{ width: `${percent}%` }} />}</span>
      <span className={styles.time}>
        {formatDuration(progress.positionSeconds)}
        {duration === null ? null : ` / ${formatDuration(duration)}`}
      </span>
    </span>
  );
}

type ResumeLayoutProps = {
  kicker: string;
  playing: boolean;
  albumTitle: string;
  groupId: string;
  releaseId: string;
  sub: string;
  artworkId: string | null;
  extra: ReactNode;
  button: ReactNode;
  status?: ReactNode;
};

function ResumeLayout({ kicker, playing, albumTitle, groupId, releaseId, sub, artworkId, extra, button, status }: ResumeLayoutProps) {
  const headingId = useId();
  return (
    <section className={styles.resume} aria-labelledby={headingId}>
      <div className={styles.card}>
        <ResumeArtwork artworkId={artworkId} />
        <div className={styles.text}>
          <h2 id={headingId} className={styles.kicker}>
            <Bars playing={playing} />
            {kicker}
          </h2>
          <p className={styles.album}>
            <Link to="/albums/$groupId" params={{ groupId }} search={{ release: releaseId }}>
              {albumTitle}
            </Link>
          </p>
          <p className={styles.sub}>{sub}</p>
          {extra}
        </div>
        <span className={styles.play}>{button}</span>
      </div>
      {status}
    </section>
  );
}

function CurrentResume({ track }: { track: PlayerTrack }) {
  const engine = usePlayerEngine();
  // 与播放栏的播放键同一判定：播放区的意图是播放时按下去是暂停。
  const playing = usePlayerSelector((snapshot) => snapshot.zone?.intent === "play");
  return (
    <ResumeLayout
      kicker={playing ? "正在播放" : "继续播放"}
      playing={playing}
      albumTitle={track.albumTitle}
      groupId={track.groupId}
      releaseId={track.releaseId}
      sub={`${track.albumArtist} · ${track.title}`}
      artworkId={track.artworkId}
      extra={<CurrentProgress />}
      button={
        <Button
          variant="icon"
          icon={playing ? <Pause size={22} fill="currentColor" /> : <Play size={22} fill="currentColor" />}
          // 名称带专辑名：播放栏也有“暂停”，同一页两个同名按钮读屏分不清。
          label={playing ? `暂停《${track.albumTitle}》` : `继续播放《${track.albumTitle}》`}
          onClick={() => engine.togglePlayback()}
        />
      }
    />
  );
}

function LastPlayedResume({ item }: { item: RecentReleaseDTO }) {
  const starter = useReleaseStarter();
  const { release } = item;
  const trackTitle = item.last_track.title.trim() === "" ? "（无标题）" : item.last_track.title;
  return (
    <ResumeLayout
      kicker="继续播放"
      playing={false}
      albumTitle={release.title}
      groupId={item.group_id}
      releaseId={release.id}
      sub={`${albumArtistOf(release)} · ${trackTitle}`}
      artworkId={release.artwork?.resource_id ?? null}
      extra={
        <p className={styles.when}>
          上次播放 <time dateTime={item.last_played_at}>{formatRelativeTime(item.last_played_at)}</time>
        </p>
      }
      button={
        <Button
          variant="icon"
          icon={<Play size={22} fill="currentColor" />}
          label={`从《${trackTitle}》继续播放`}
          disabled={!starter.ready}
          busy={starter.pending !== null}
          onClick={() => void starter.start({ groupId: item.group_id, releaseId: release.id, title: release.title, trackId: item.last_track.id })}
        />
      }
      status={starter.status}
    />
  );
}

/**
 * 概览页首的“继续播放”：有当前曲目时显示它所在的专辑、曲目、真实进度与播放/暂停；没有当前曲目时取最近播放的第一张，
 * 按下从上次播放的那一首开始放这张专辑。都没有时不渲染；播放器还在读取播放区时也先不渲染，避免先闪出上次播放再换成当前曲目。
 */
export function ResumeCard() {
  const current = usePlayerSelector((snapshot) => snapshot.current);
  const restoring = usePlayerSelector((snapshot) => snapshot.restoring);
  const recent = useRecentReleases();
  if (restoring) return null;
  if (current !== null) return <CurrentResume track={current.track} />;
  const last = recent.data?.items[0];
  return last === undefined ? null : <LastPlayedResume item={last} />;
}
