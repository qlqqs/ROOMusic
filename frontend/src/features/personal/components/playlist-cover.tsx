import { Cover } from "../../catalog";
import type { PlaylistSummaryDTO } from "../api/decoders";
import styles from "./playlist-cover.module.css";

/**
 * 歌单的封面：前四张不同专辑的封面拼成方格；不足四张时用第一张，没有时是标题文字版面。装饰性的（名称在旁边的链接里）。
 * 第一张专辑没有封面时显示文字版面，不跳过它去找后面有封面的专辑。
 */
export function PlaylistCover({ playlist }: { playlist: Pick<PlaylistSummaryDTO, "name" | "cover_groups"> }) {
  const groups = playlist.cover_groups;
  if (groups.length === 4) {
    return (
      <div className={styles.mosaic} aria-hidden="true">
        {groups.map((group) => (
          <Cover key={group.group_id} artwork={group.artwork} title={playlist.name} variant="card" />
        ))}
      </div>
    );
  }
  return <Cover artwork={groups[0]?.artwork ?? null} title={playlist.name} variant="card" />;
}
