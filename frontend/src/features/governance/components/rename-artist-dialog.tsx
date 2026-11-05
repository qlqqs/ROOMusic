import { useId, useRef, useState, type FormEvent } from "react";
import { Button, ModalDialog, TextField } from "../../../shared/ui";
import type { ArtistCommands, ArtistTarget } from "../hooks/use-artist-commands";
import { ArtistDialogFailure } from "./artist-command-notice";
import styles from "./artist-dialog.module.css";

/** 主名的上限（UTF-8 字节，与署名原文的上限一致）。 */
const MAX_ARTIST_NAME_BYTES = 4096;

type RenameTarget = ArtistTarget & { revision: number; nameSource: "auto" | "manual" };

type RenameArtistDialogProps = { open: boolean; artist: RenameTarget; commands: ArtistCommands; onClose: () => void };

/**
 * 修改主名（管理员）：写一个主名，或在已经改过时改回按最常见的写法自动显示。原来的名字仍然能搜到。
 * 名字只去掉首尾空白；和别的艺人同名也可以（不会因此合并）。不能撤销，需要时再改一次。
 */
export function RenameArtistDialog({ open, artist, commands, onClose }: RenameArtistDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "rename";
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (pending ? undefined : onClose())}>
      <RenameForm titleId={titleId} headingRef={headingRef} artist={artist} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}

function RenameForm({ titleId, headingRef, artist, commands, onClose }: Omit<RenameArtistDialogProps, "open"> & { titleId: string; headingRef: React.RefObject<HTMLHeadingElement | null> }) {
  const [mode, setMode] = useState<"set" | "auto">("set");
  const [draft, setDraft] = useState(artist.name);
  const [error, setError] = useState<string | null>(null);
  const radioName = useId();
  const state = commands.state;
  const pending = state.status === "pending" && state.command.kind === "rename";
  const failed = state.status === "failed" && state.command.kind === "rename" && state.command.artist.id === artist.id ? state : null;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    if (mode === "auto") {
      commands.submit({ kind: "rename", artist: { id: artist.id, name: artist.name }, revision: artist.revision, name: { mode: "auto" } }, { onSucceeded: onClose });
      return;
    }
    const name = draft.trim();
    if (name === "" || new TextEncoder().encode(name).length > MAX_ARTIST_NAME_BYTES || /\p{Cc}/u.test(name)) {
      setError("请写一个名字，不要太长。");
      return;
    }
    setError(null);
    commands.submit({ kind: "rename", artist: { id: artist.id, name: artist.name }, revision: artist.revision, name: { mode: "set", name } }, { onSucceeded: onClose });
  };

  return (
    <form className={styles.content} onSubmit={submit}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        修改主名
      </h2>
      {artist.nameSource === "manual" ? (
        <fieldset className={styles.section} disabled={pending}>
          <legend className={styles.legend}>主名</legend>
          <label className={styles.option}>
            <input type="radio" name={radioName} checked={mode === "set"} onChange={() => setMode("set")} />
            <span className={styles.optionText}>使用下面的名字</span>
          </label>
          <label className={styles.option}>
            <input type="radio" name={radioName} checked={mode === "auto"} onChange={() => setMode("auto")} />
            <span className={styles.optionText}>改回最常见的写法</span>
          </label>
        </fieldset>
      ) : null}
      {mode === "set" ? (
        <div className={styles.nameField}>
          <TextField label="主名" type="text" autoComplete="off" value={draft} error={error} disabled={pending} onChange={(event) => setDraft(event.target.value)} />
        </div>
      ) : null}
      <div className={styles.footer}>
        <p className={styles.hint}>改名不能撤销，需要时可以再改。</p>
        <ArtistDialogFailure state={state} kind="rename" />
        <div className={styles.actions}>
          <Button
            onClick={() => {
              commands.dismiss();
              onClose();
            }}
            disabled={pending}
          >
            取消
          </Button>
          <Button type="submit" variant="primary" busy={pending}>
            {pending ? "正在保存…" : failed?.uncertain ? "重试保存" : "保存"}
          </Button>
        </div>
      </div>
    </form>
  );
}
