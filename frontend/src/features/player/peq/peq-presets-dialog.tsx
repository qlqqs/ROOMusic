import { Check, Pencil, Trash2, X } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Button, ConfirmDialog, FailureNotice, ModalDialog } from "../../../shared/ui";
import type { PeqPreset } from "./api";
import type { PeqDocument } from "./document";
import { usePeqPresetActions, usePeqPresets } from "./hooks";
import styles from "./peq.module.css";

const presetMessages = { peq_preset_name_taken: "已经有同名的预设。", peq_preset_limit: "预设已满 200 个，先删掉一些。" };

function presetName(text: string): string | null {
  const name = text.trim();
  return name === "" || [...name].length > 64 ? null : name;
}

function PresetRow({ preset, onApply, onDelete }: { preset: PeqPreset; onApply: () => void; onDelete: () => void }) {
  const { rename } = usePeqPresetActions();
  const [editing, setEditing] = useState<string | null>(null);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const name = editing === null ? null : presetName(editing);
    if (name === null) return;
    if (name === preset.name) {
      setEditing(null);
      return;
    }
    rename.mutate({ presetId: preset.presetId, name }, { onSuccess: () => setEditing(null) });
  };
  if (editing !== null) {
    return (
      <li className={styles.presetRow}>
        <form className={styles.rename} onSubmit={submit}>
          <input className={styles.input} aria-label="预设名称" value={editing} maxLength={64} autoFocus onChange={(event) => setEditing(event.target.value)} />
          <Button variant="icon" type="submit" icon={<Check size={16} />} label="保存名称" busy={rename.isPending} />
          <Button variant="icon" icon={<X size={16} />} label="取消" onClick={() => setEditing(null)} />
        </form>
        {rename.isError ? <FailureNotice error={rename.error} context="write" messages={presetMessages} /> : null}
      </li>
    );
  }
  return (
    <li className={styles.presetRow}>
      <span className={styles.presetName}>{preset.name}</span>
      <Button onClick={onApply}>套用</Button>
      <Button variant="icon" icon={<Pencil size={16} />} label="改名" onClick={() => setEditing(preset.name)} />
      <Button variant="icon" icon={<Trash2 size={16} />} label="删除" onClick={onDelete} />
    </li>
  );
}

/** 预设：套用、改名、删除，把当前参数存为预设。 */
export function PeqPresetsDialog({ open, current, onApply, onClose }: { open: boolean; current: PeqDocument; onApply: (peq: PeqDocument) => void; onClose: () => void }) {
  const titleId = useId();
  return (
    <ModalDialog open={open} titleId={titleId} className={styles.subDialog} onRequestClose={onClose}>
      <div className={styles.panel}>
        <div className={styles.header}>
          <h2 id={titleId} className={styles.title}>预设</h2>
          <div className={styles.toolbar}><Button variant="icon" icon={<X size={18} />} label="关闭" onClick={onClose} /></div>
        </div>
        <PresetsBody current={current} onApply={onApply} />
      </div>
    </ModalDialog>
  );
}

function PresetsBody({ current, onApply }: { current: PeqDocument; onApply: (peq: PeqDocument) => void }) {
  const presets = usePeqPresets();
  const { create, remove } = usePeqPresetActions();
  const [name, setName] = useState("");
  const [deleting, setDeleting] = useState<PeqPreset | null>(null);
  const save = (event: FormEvent) => {
    event.preventDefault();
    const valid = presetName(name);
    if (valid === null) return;
    create.mutate({ name: valid, peq: current }, { onSuccess: () => setName("") });
  };
  return (
    <div className={styles.panelBody}>
      {presets.isError ? <FailureNotice error={presets.error} /> : null}
      <ul className={styles.list}>
        {presets.data?.map((preset) => (
          <PresetRow
            key={preset.presetId}
            preset={preset}
            onApply={() => onApply({ ...preset.peq, source: { kind: "preset", presetId: preset.presetId, name: preset.name } })}
            onDelete={() => setDeleting(preset)}
          />
        ))}
      </ul>
      <form className={styles.rename} onSubmit={save}>
        <input className={styles.input} aria-label="新预设名称" placeholder="存为预设" value={name} maxLength={64} onChange={(event) => setName(event.target.value)} />
        <Button type="submit" busy={create.isPending} disabled={presetName(name) === null}>保存</Button>
      </form>
      {create.isError ? <FailureNotice error={create.error} context="write" messages={presetMessages} /> : null}
      <ConfirmDialog
        open={deleting !== null}
        title="删除预设"
        confirmLabel="删除"
        pending={remove.isPending}
        error={remove.isError ? <FailureNotice error={remove.error} context="write" /> : undefined}
        onConfirm={() => deleting !== null && remove.mutate(deleting.presetId, { onSuccess: () => setDeleting(null) })}
        onCancel={() => {
          remove.reset();
          setDeleting(null);
        }}
      >
        删除“{deleting?.name}”？
      </ConfirmDialog>
    </div>
  );
}
