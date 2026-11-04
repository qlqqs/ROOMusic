import { useEffect, useEffectEvent, useId, useState, type FormEvent, type ReactNode } from "react";
import { Button, FailureNotice, ModalDialog, TextField } from "../../../shared/ui";
import type { PlaylistCreatedDTO } from "../api/decoders";
import { useCreatePlaylist } from "../hooks/use-playlists";
import { normalizePlaylistName, playlistNameProblem } from "../playlist-name";
import { playlistMessages } from "../playlist-text";
import styles from "./personal-dialog.module.css";

type CreatePlaylistFormProps = {
  initialName: string;
  trackIds: readonly string[];
  submitLabel: string;
  /** 表单下方的另一个按钮（“取消”或“返回”）；提交中由调用方禁用。 */
  secondary: (pending: boolean) => ReactNode;
  onCreated: (result: PlaylistCreatedDTO) => void;
  /** 提交中的变化：对话框据此在提交中忽略 Esc。 */
  onPendingChange?: ((pending: boolean) => void) | undefined;
};

/**
 * 新建歌单的表单：只要名字（必填，最多 100 个字，可以重名）。曲目与新建在同一个请求里，不会建成空歌单后第二步失败。
 * 结果不确定时“重试”沿用同一个请求；改了名字再提交就是新的一次。
 */
export function CreatePlaylistForm({ initialName, trackIds, submitLabel, secondary, onCreated, onPendingChange }: CreatePlaylistFormProps) {
  const create = useCreatePlaylist();
  const [name, setName] = useState(initialName);
  const [showProblem, setShowProblem] = useState(false);
  const problem = playlistNameProblem(name);
  const pending = create.state.status === "pending";
  const reportPending = useEffectEvent((value: boolean) => onPendingChange?.(value));
  useEffect(() => {
    reportPending(pending);
    return () => reportPending(false);
  }, [pending]);
  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    if (problem !== null) {
      setShowProblem(true);
      return;
    }
    create.run({ name: normalizePlaylistName(name), trackIds }, { onSucceeded: onCreated });
  }
  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <TextField
        label="名字"
        value={name}
        autoComplete="off"
        error={showProblem ? problem : null}
        onChange={(event) => {
          setName(event.target.value);
          if (create.state.status === "failed") create.dismiss();
        }}
      />
      {create.state.status === "failed" ? (
        <FailureNotice
          error={create.state.error}
          context="write"
          messages={playlistMessages}
          action={create.state.uncertain ? <Button onClick={() => create.retry({ onSucceeded: onCreated })}>重试</Button> : undefined}
        />
      ) : null}
      <div className={styles.actions}>
        {secondary(pending)}
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在新建…" : submitLabel}
        </Button>
      </div>
    </form>
  );
}

type CreatePlaylistDialogProps = {
  open: boolean;
  title: string;
  initialName?: string;
  /** 新建时一起放进去的曲目（已去重，保持顺序）；只建空歌单时省略。 */
  trackIds?: readonly string[];
  submitLabel: string;
  onCreated: (result: PlaylistCreatedDTO) => void;
  onClose: () => void;
};

const noTracks: readonly string[] = [];

/** 新建歌单的对话框（歌单页的“新建歌单”、队列的“存为歌单”）。提交中不能关闭。 */
export function CreatePlaylistDialog({ open, title, initialName = "", trackIds = noTracks, submitLabel, onCreated, onClose }: CreatePlaylistDialogProps) {
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
          {title}
        </h2>
      </div>
      <CreatePlaylistForm
        initialName={initialName}
        trackIds={trackIds}
        submitLabel={submitLabel}
        secondary={(busy) => (
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
        )}
        onCreated={onCreated}
        onPendingChange={setPending}
      />
    </ModalDialog>
  );
}
