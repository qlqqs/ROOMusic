import { Check, Pencil, Trash2, Upload, X } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { failureOf } from "../../../shared/api";
import { Button, ConfirmDialog, FailureNotice, ModalDialog } from "../../../shared/ui";
import { usePlayerSelector } from "../player-provider";
import { IR_ACCEPT, type IrFile } from "./api";
import { useIrFileActions, useIrFiles } from "./hooks";
import styles from "./convolution.module.css";

const uploadMessages = {
  ir_format_unsupported: "不支持这种文件。",
  ir_decode_failed: "文件读不出来。",
  ir_too_large: "文件太大，最多 32 MB。",
  ir_limit_reached: "IR 文件已满 50 个，先删掉一些。",
};

function irName(text: string): string | null {
  const name = text.trim();
  return name === "" || [...name].length > 64 ? null : name;
}

/** 默认名称：文件名去掉扩展名，过长截断。 */
function nameOfFile(fileName: string): string {
  const stem = fileName.replace(/\.[^.]+$/, "").trim();
  return [...(stem === "" ? fileName : stem)].slice(0, 64).join("");
}

export function rateLabel(sampleRate: number): string {
  const khz = sampleRate / 1000;
  return `${Number.isInteger(khz) ? khz : khz.toFixed(1)} kHz`;
}

function IrRow({ file, onDelete }: { file: IrFile; onDelete: () => void }) {
  const { rename } = useIrFileActions();
  const [editing, setEditing] = useState<string | null>(null);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const name = editing === null ? null : irName(editing);
    if (name === null) return;
    if (name === file.name) {
      setEditing(null);
      return;
    }
    rename.mutate({ irId: file.irId, name }, { onSuccess: () => setEditing(null) });
  };
  if (editing !== null) {
    return (
      <li className={styles.row}>
        <form className={styles.inline} onSubmit={submit}>
          <input className={styles.input} aria-label="IR 名称" value={editing} maxLength={64} autoFocus onChange={(event) => setEditing(event.target.value)} />
          <Button variant="icon" type="submit" icon={<Check size={16} />} label="保存名称" busy={rename.isPending} />
          <Button variant="icon" icon={<X size={16} />} label="取消" onClick={() => setEditing(null)} />
        </form>
        {rename.isError ? <FailureNotice error={rename.error} context="write" /> : null}
      </li>
    );
  }
  return (
    <li className={styles.row}>
      <div className={styles.text}>
        <span className={styles.name}>{file.name}</span>
        <span className={styles.meta}>{rateLabel(file.sampleRate)} · {file.channels} 声道 · {file.taps} 抽头</span>
      </div>
      <Button variant="icon" icon={<Pencil size={16} />} label="改名" onClick={() => setEditing(file.name)} />
      <Button variant="icon" icon={<Trash2 size={16} />} label="删除" onClick={onDelete} />
    </li>
  );
}

function IrFilesBody() {
  const files = useIrFiles();
  const { upload, progress, cancelUpload, remove } = useIrFileActions();
  const inUse = usePlayerSelector((snapshot) => snapshot.settings.convolution.irId);
  const [deleting, setDeleting] = useState<IrFile | null>(null);
  const [uploading, setUploading] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const failure = upload.isError ? failureOf(upload.error) : null;
  const pick = (file: File | undefined) => {
    if (file === undefined) return;
    setUploading(file.name);
    upload.mutate({ name: nameOfFile(file.name), file });
  };
  return (
    <div className={styles.body}>
      {files.isError ? <FailureNotice error={files.error} /> : null}
      {files.data?.length === 0 ? <p className={styles.meta}>还没有 IR 文件</p> : null}
      <ul className={styles.list}>
        {files.data?.map((file) => <IrRow key={file.irId} file={file} onDelete={() => setDeleting(file)} />)}
      </ul>
      {upload.isPending ? (
        <div className={styles.progress}>
          <span className={styles.name}>{uploading}</span>
          <progress max={1} value={progress} aria-label="上传进度" />
          <span className={styles.meta}>{Math.round(progress * 100)}%</span>
          <Button variant="icon" icon={<X size={16} />} label="取消上传" onClick={cancelUpload} />
        </div>
      ) : null}
      {upload.isError && failure?.kind !== "aborted" ? (
        <FailureNotice error={upload.error} context="write" messages={failure?.kind === "http" && failure.code === "ir_cfg_invalid" ? { ir_cfg_invalid: failure.message } : uploadMessages} />
      ) : null}
      <div className={styles.actions}>
        <input
          ref={picker}
          type="file"
          accept={IR_ACCEPT}
          hidden
          onChange={(event) => {
            pick(event.target.files?.[0]);
            event.target.value = "";
          }}
        />
        <Button icon={<Upload size={16} />} disabled={upload.isPending} onClick={() => picker.current?.click()}>上传</Button>
      </div>
      <ConfirmDialog
        open={deleting !== null}
        title="删除 IR 文件"
        confirmLabel="删除"
        pending={remove.isPending}
        error={remove.isError ? <FailureNotice error={remove.error} context="write" /> : undefined}
        onConfirm={() => deleting !== null && remove.mutate(deleting.irId, { onSuccess: () => setDeleting(null) })}
        onCancel={() => {
          remove.reset();
          setDeleting(null);
        }}
      >
        删除“{deleting?.name}”？{deleting !== null && deleting.irId === inUse ? "正在用它的卷积会关掉。" : null}
      </ConfirmDialog>
    </div>
  );
}

/** IR 文件管理：上传、改名、删除。 */
export function IrFilesDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const titleId = useId();
  return (
    <ModalDialog open={open} titleId={titleId} className={styles.dialog} onRequestClose={onClose}>
      <div className={styles.panel}>
        <div className={styles.header}>
          <h2 id={titleId} className={styles.title}>IR 文件</h2>
          <Button variant="icon" icon={<X size={18} />} label="关闭" onClick={onClose} />
        </div>
        {open ? <IrFilesBody /> : null}
      </div>
    </ModalDialog>
  );
}
