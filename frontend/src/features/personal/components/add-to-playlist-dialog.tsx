import { useId, useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button, FailureNotice, ModalDialog, Skeleton } from "../../../shared/ui";
import { PERSONAL_TRACK_LIMIT, type PlaylistSummaryDTO } from "../api/decoders";
import { useAddToPlaylist, usePlaylistPicker, usePlaylistsContainingTrack } from "../hooks/use-playlists";
import { addedText, playlistMessages } from "../playlist-text";
import { CreatePlaylistForm } from "./create-playlist-dialog";
import styles from "./personal-dialog.module.css";

type AddToPlaylistDialogProps = {
  open: boolean;
  /** 要加入的曲目（打开时的快照，保持顺序）。 */
  trackIds: readonly string[];
  /** 加入的是什么，写在标题下面，例如“《曲名》”“这张专辑的 12 首”。 */
  subject: string;
  /** 加入成功：对话框关闭，提示由打开它的地方显示在原处。 */
  onAdded: (message: string) => void;
  onClose: () => void;
};

/**
 * “添加到歌单…”：歌单按最近修改倒序，一首时已包含它的歌单标“已在歌单里”；可以直接新建歌单并加入。
 * 一次只加入一个歌单，提交中不能关闭；结果不确定时“重试”沿用同一个请求。
 */
export function AddToPlaylistDialog({ open, trackIds, subject, onAdded, onClose }: AddToPlaylistDialogProps) {
  const titleId = useId();
  const [pending, setPending] = useState(false);
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending) onClose();
      }}
    >
      <div className={styles.header}>
        <h2 id={titleId} className={styles.title}>
          添加到歌单
        </h2>
        <p className={styles.subject}>{subject}</p>
      </div>
      <AddToPlaylistBody trackIds={trackIds} onAdded={onAdded} onClose={onClose} onPendingChange={setPending} />
    </ModalDialog>
  );
}

type BodyProps = Pick<AddToPlaylistDialogProps, "trackIds" | "onAdded" | "onClose"> & { onPendingChange: (pending: boolean) => void };

function AddToPlaylistBody({ trackIds, onAdded, onClose, onPendingChange }: BodyProps) {
  const [creating, setCreating] = useState(false);
  const single = trackIds.length === 1;
  if (creating) {
    return (
      <CreatePlaylistForm
        initialName=""
        trackIds={trackIds}
        submitLabel="新建并加入"
        secondary={(busy) => (
          <Button onClick={() => setCreating(false)} disabled={busy}>
            返回
          </Button>
        )}
        onPendingChange={onPendingChange}
        onCreated={(result) => onAdded(single ? `已新建《${result.playlist.name}》并加入。` : `已新建《${result.playlist.name}》，加入了 ${formatCount(result.added_count)} 首。`)}
      />
    );
  }
  return <PlaylistChoices trackIds={trackIds} onAdded={onAdded} onClose={onClose} onCreate={() => setCreating(true)} onPendingChange={onPendingChange} />;
}

function PlaylistChoices({ trackIds, onAdded, onClose, onCreate, onPendingChange }: BodyProps & { onCreate: () => void }) {
  const single = trackIds.length === 1;
  const picker = usePlaylistPicker();
  const containing = usePlaylistsContainingTrack(single ? (trackIds[0] ?? null) : null);
  const add = useAddToPlaylist();
  const [chosen, setChosen] = useState<string | null>(null);
  const pending = add.state.status === "pending";
  const playlists = picker.data?.pages.flatMap((page) => page.data.items) ?? [];
  const succeeded = (playlist: PlaylistSummaryDTO) => (result: { added_count: number; existing_count: number }) => {
    onPendingChange(false);
    onAdded(addedText(playlist.name, result, single));
  };
  function choose(playlist: PlaylistSummaryDTO) {
    if (pending) return;
    setChosen(playlist.id);
    onPendingChange(true);
    add.run(
      { playlistId: playlist.id, expectedRevision: playlist.revision, trackIds },
      {
        onSucceeded: succeeded(playlist),
        onFailed: (error) => {
          onPendingChange(false);
          // 歌单刚改过：重新读列表拿到新的版本，用户再选一次。
          if (isHttpFailure(failureOf(error), 409, "revision_conflict")) void picker.refetch();
        },
      },
    );
  }
  function retry() {
    const playlist = playlists.find((item) => item.id === chosen);
    if (playlist === undefined) return;
    onPendingChange(true);
    add.retry({ onSucceeded: succeeded(playlist), onFailed: () => onPendingChange(false) });
  }
  return (
    <div className={styles.content}>
      {picker.data === undefined ? (
        picker.isError ? (
          <FailureNotice error={picker.error} action={<Button onClick={() => void picker.refetch()}>重试</Button>} />
        ) : (
          <Skeleton label="正在读取歌单" lines={3} />
        )
      ) : playlists.length === 0 ? (
        <p>还没有歌单。</p>
      ) : (
        <ul className={styles.list} aria-label="歌单">
          {playlists.map((playlist) => {
            const present = containing.data?.has(playlist.id) === true;
            const full = playlist.track_count >= PERSONAL_TRACK_LIMIT;
            const state = present ? "已在歌单里" : full ? "已满" : null;
            return (
              <li key={playlist.id}>
                <button
                  type="button"
                  className={styles.option}
                  disabled={state !== null || (pending && chosen !== playlist.id)}
                  aria-busy={pending && chosen === playlist.id ? true : undefined}
                  onClick={() => choose(playlist)}
                >
                  <span className={styles.optionName}>{playlist.name}</span>
                  <span className={styles.optionMeta}>
                    {formatCount(playlist.track_count)} 首{state === null ? "" : ` · ${state}`}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {picker.hasNextPage ? (
        <span className={styles.more}>
          <Button busy={picker.isFetchingNextPage} onClick={() => void picker.fetchNextPage()}>
            {picker.isFetchingNextPage ? "正在读取…" : "显示更多歌单"}
          </Button>
        </span>
      ) : null}
      {add.state.status === "failed" ? (
        <FailureNotice
          error={add.state.error}
          context="write"
          messages={playlistMessages}
          action={add.state.uncertain ? <Button onClick={retry}>重试</Button> : undefined}
        />
      ) : null}
      <div className={styles.actions}>
        <Button onClick={onClose} disabled={pending}>
          取消
        </Button>
        <Button variant="primary" disabled={pending} onClick={onCreate}>
          新建歌单
        </Button>
      </div>
    </div>
  );
}
