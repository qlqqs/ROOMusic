import { Disc3 } from "lucide-react";
import { useState } from "react";
import { artworkPath } from "../../catalog";
import type { PlayerTrack } from "../engine/queue";
import styles from "./player-artwork.module.css";

/** 保留原图比例；没有封面或加载失败时保留同尺寸的唱片标记。 */
export function PlayerArtwork({ track }: { track: PlayerTrack }) {
  const [failedId, setFailedId] = useState<string | null>(null);
  return <div className={styles.cover}>
    {track.artworkId === null || failedId === track.artworkId ? <Disc3 aria-label="没有可用封面" size={32} /> : <img src={artworkPath(track.artworkId)} alt={`《${track.albumTitle}》封面`} onError={() => setFailedId(track.artworkId)} />}
  </div>;
}
