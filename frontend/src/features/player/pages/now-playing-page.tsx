import { Link } from "@tanstack/react-router";
import { Page } from "../../../shared/ui";
import { usePlayer } from "../player-provider";
import { PlayerArtwork } from "../components/player-artwork";
import { PlayerControls } from "../components/player-controls";
import { PlayerFeedback } from "../components/player-feedback";
import { PlayerQueue } from "../components/player-queue";
import { SignalPath } from "../components/signal-path";
import { statusLabels } from "../components/player-labels";
import styles from "./now-playing-page.module.css";

export function NowPlayingPage() {
  const { snapshot } = usePlayer();
  const current = snapshot.current;
  return <Page title="正在播放" actions={<Link to="/albums">去专辑</Link>}>
    <PlayerFeedback />
    {snapshot.restoring ? <p role="status">正在恢复播放队列…</p> : null}
    {snapshot.queue.entries.length === 0 ? <p>队列是空的。去专辑选择一张唱片，开始聆听。</p> : <div className={styles.page}>
      {current === null ? <p>请选择队列中的一项播放。</p> : <div className={styles.hero}>
        <div className={styles.artwork}><PlayerArtwork track={current.track} /></div>
        <div className={styles.info}>
          <p>{statusLabels[snapshot.status]} · 第 {snapshot.queue.entries.findIndex((entry) => entry.entryId === current.entryId) + 1} 首 / 共 {snapshot.queue.entries.length} 首</p>
          <h2 className={styles.title} title={current.track.title}>{current.track.title}</h2>
          <p className={styles.artist} title={current.track.artist}>{current.track.artist}</p>
          <p className={styles.album}><Link to="/albums/$groupId" params={{ groupId: current.track.groupId }} search={{ release: current.track.releaseId }} title={current.track.albumTitle}>{current.track.albumTitle}</Link></p>
          <PlayerControls />
        </div>
      </div>}
      <SignalPath /><PlayerQueue />
    </div>}
  </Page>;
}
