import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useRef, useState } from "react";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { formatCount } from "../../../shared/lib/number";
import { Button, Pagination, Panel, QueryContent } from "../../../shared/ui";
import { AlbumGridLayout, AlbumGridSkeleton } from "../../catalog";
import type { PlaylistSummaryDTO } from "../api/decoders";
import { usePlaylists } from "../hooks/use-playlists";
import { playlistMetaText } from "../playlist-text";
import { CreatePlaylistDialog } from "./create-playlist-dialog";
import { PlaylistCover } from "./playlist-cover";
import styles from "./playlists-view.module.css";

function PlaylistCard({ playlist }: { playlist: PlaylistSummaryDTO }) {
  return (
    <li className={styles.item}>
      <Link to="/me/playlists/$playlistId" params={{ playlistId: playlist.id }} className={styles.card}>
        <PlaylistCover playlist={playlist} />
        <span className={styles.name}>{playlist.name}</span> <span className={styles.meta}>{playlistMetaText(playlist)}</span>
      </Link>
    </li>
  );
}

/**
 * 我的歌单：本人的歌单，按最近修改倒序，卡片显示封面、名字、曲目数与总时长。
 * “新建歌单”只要名字，建好后直接进入这个歌单。
 */
export function PlaylistsView({ page }: { page: number }) {
  const list = usePlaylists(page);
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const ready = list.data !== undefined && !list.isPlaceholderData;
  const pageCount = pageCountOf(list.data?.pagination.total ?? 0, list.data?.pagination.page_size ?? 1);
  const outOfRange = useClampedPage({
    page,
    pageCount,
    ready,
    onOutOfRange: (target) => void navigate({ to: "/me/playlists", search: { page: target > 1 ? target : undefined }, replace: true }),
  });
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  return (
    <>
      <Panel
        title="全部歌单"
        headingRef={headingRef}
        action={
          <Button icon={<Plus size={16} />} onClick={() => setCreating(true)}>
            新建歌单
          </Button>
        }
      >
        <QueryContent query={list} loading={<AlbumGridSkeleton />}>
          {(data) =>
            outOfRange ? (
              <AlbumGridSkeleton />
            ) : data.pagination.total === 0 ? (
              <p>还没有歌单。</p>
            ) : (
              <div className={styles.results} aria-busy={list.isPlaceholderData}>
                <AlbumGridLayout>
                  {data.items.map((playlist) => (
                    <PlaylistCard key={playlist.id} playlist={playlist} />
                  ))}
                </AlbumGridLayout>
                <p className={styles.note}>共 {formatCount(data.pagination.total)} 个歌单</p>
                <Pagination
                  label="歌单分页"
                  page={data.pagination.page}
                  pageCount={pageCount}
                  renderLink={(target, props) => <Link to="/me/playlists" search={{ page: target > 1 ? target : undefined }} onClick={showTop} {...props} />}
                />
              </div>
            )
          }
        </QueryContent>
      </Panel>
      <CreatePlaylistDialog
        open={creating}
        title="新建歌单"
        submitLabel="新建"
        onClose={() => setCreating(false)}
        onCreated={(result) => {
          setCreating(false);
          void navigate({ to: "/me/playlists/$playlistId", params: { playlistId: result.playlist.id } });
        }}
      />
    </>
  );
}
