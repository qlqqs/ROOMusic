import { useId, useState, type FormEvent } from "react";
import { Button, FailureNotice, ModalDialog, TextField } from "../../../shared/ui";
import type { PlaylistRenamedDTO } from "../api/decoders";
import type { CommandCallbacks, CommandState } from "../hooks/use-idempotent-command";
import { normalizePlaylistName, playlistNameProblem } from "../playlist-name";
import { playlistMessages } from "../playlist-text";
import styles from "./personal-dialog.module.css";

type RenameCommand = {
  state: CommandState;
  run: (input: { expectedRevision: number; name: string }, callbacks?: CommandCallbacks<PlaylistRenamedDTO>) => void;
  retry: (callbacks?: CommandCallbacks<PlaylistRenamedDTO>) => void;
  dismiss: () => void;
};

type RenamePlaylistDialogProps = {
  open: boolean;
  name: string;
  revision: number;
  command: RenameCommand;
  onClose: () => void;
};

/** 改名：名字规则与新建相同；提交中不能关闭，失败留在对话框里。 */
export function RenamePlaylistDialog({ open, name, revision, command, onClose }: RenamePlaylistDialogProps) {
  const titleId = useId();
  const pending = command.state.status === "pending";
  const close = () => {
    command.dismiss();
    onClose();
  };
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending) close();
      }}
    >
      <div className={styles.header}>
        <h2 id={titleId} className={styles.title}>
          改名
        </h2>
      </div>
      <RenameForm name={name} revision={revision} command={command} onDone={close} />
    </ModalDialog>
  );
}

function RenameForm({ name: initial, revision, command, onDone }: { name: string; revision: number; command: RenameCommand; onDone: () => void }) {
  const [name, setName] = useState(initial);
  const [showProblem, setShowProblem] = useState(false);
  const problem = playlistNameProblem(name);
  const pending = command.state.status === "pending";
  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    if (problem !== null) {
      setShowProblem(true);
      return;
    }
    command.run({ expectedRevision: revision, name: normalizePlaylistName(name) }, { onSucceeded: onDone });
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
          if (command.state.status === "failed") command.dismiss();
        }}
      />
      {command.state.status === "failed" ? (
        <FailureNotice
          error={command.state.error}
          context="write"
          messages={playlistMessages}
          action={command.state.uncertain ? <Button onClick={() => command.retry({ onSucceeded: onDone })}>重试</Button> : undefined}
        />
      ) : null}
      <div className={styles.actions}>
        <Button onClick={onDone} disabled={pending}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在保存…" : "保存"}
        </Button>
      </div>
    </form>
  );
}
