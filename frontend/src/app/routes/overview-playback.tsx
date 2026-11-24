import { Link } from "@tanstack/react-router";
import { Disc3, Play } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { albumArtistOf, artworkPath, useReleaseLoader } from "../../features/catalog";
import { useRecentReleases, type RecentReleaseDTO } from "../../features/listening";
import { playerTracksOf, usePlaybackCapabilities, usePlayerEngine, usePlayerSelector } from "../../features/player";
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

type ResumeLayoutProps = {
  kicker: string;
  albumTitle: string;
  groupId: string;
  releaseId: string;
  sub: string;
  artworkId: string | null;
  extra: ReactNode;
  button: ReactNode;
  status?: ReactNode;
};

function ResumeLayout({ kicker, albumTitle, groupId, releaseId, sub, artworkId, extra, button, status }: ResumeLayoutProps) {
  const headingId = useId();
  return (
    <section className={styles.resume} aria-labelledby={headingId}>
      <div className={styles.card}>
        <ResumeArtwork artworkId={artworkId} />
        <div className={styles.text}>
          <h2 id={headingId} className={styles.kicker}>
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

function LastPlayedResume({ item }: { item: RecentReleaseDTO }) {
  const starter = useReleaseStarter();
  const { release } = item;
  const trackTitle = item.last_track.title.trim() === "" ? "（无标题）" : item.last_track.title;
  return (
    <ResumeLayout
      kicker="继续播放"
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
 * 概览页首的“继续播放”：队列是空的（底部没有播放栏）时取最近播放的第一张，按下从上次播放的那一首开始放这张专辑。
 * 有当前曲目时不渲染：播放栏（手机上是迷你栏）已经显示同一首曲目、进度与播放键，再放一张卡片只是重复。
 * 没有播放记录时不渲染；播放器还在读取播放区时也先不渲染，避免先闪出卡片再被当前曲目取代。
 */
export function ResumeCard() {
  const hasCurrent = usePlayerSelector((snapshot) => snapshot.current !== null);
  const restoring = usePlayerSelector((snapshot) => snapshot.restoring);
  const recent = useRecentReleases();
  if (restoring || hasCurrent) return null;
  const last = recent.data?.items[0];
  return last === undefined ? null : <LastPlayedResume item={last} />;
}
