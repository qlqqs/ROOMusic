import { Cover } from "../../catalog";
import type { PlaylistSummaryDTO } from "../api/decoders";
import styles from "./playlist-cover.module.css";

type PlaylistCoverProps = {
  playlist: Pick<PlaylistSummaryDTO, "name" | "cover_groups">;
  /** card：歌单页的卡片与页头，随容器宽度；thumb：侧栏歌单列表的 26px 缩略图。默认 card。 */
  size?: "card" | "thumb";
};

/**
 * 歌单的封面：前四张不同专辑的封面拼成方格；不足四张时用第一张，没有时是标题文字版面（thumb 是歌单图标）。装饰性的（名称在旁边的链接里）。
 * 第一张专辑没有封面时显示文字版面（thumb 是图标），不跳过它去找后面有封面的专辑。
 */
export function PlaylistCover({ playlist, size = "card" }: PlaylistCoverProps) {
  const groups = playlist.cover_groups;
  const variant = size === "thumb" ? "thumb" : "card";
  // 缩略图放在侧栏的链接文字旁，整个用 span（短语内容）。
  const Mosaic = size === "thumb" ? "span" : "div";
  const cover =
    groups.length === 4 ? (
      <Mosaic className={styles.mosaic} data-size={size} aria-hidden="true">
        {groups.map((group) => (
          <Cover key={group.group_id} artwork={group.artwork} title={playlist.name} variant={variant} />
        ))}
      </Mosaic>
    ) : (
      <Cover artwork={groups[0]?.artwork ?? null} title={playlist.name} variant={variant} />
    );
  if (size === "card") return cover;
  return (
    <span className={styles.thumb} aria-hidden="true">
      {cover}
    </span>
  );
}
