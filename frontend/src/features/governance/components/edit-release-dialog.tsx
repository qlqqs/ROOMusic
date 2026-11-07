import { PencilLine, TriangleAlert } from "lucide-react";
import { useId, useMemo, useRef, useState, type RefObject } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button, FailureNotice, ModalDialog, Notice, Skeleton, StatusTag, TextField } from "../../../shared/ui";
import type { CorrectableField, CorrectionFieldDTO, CorrectionStateDTO } from "../api/decoders";
import { confirmScanDraft, draftOf, planChanges, type DraftEdits, type FieldDraft, type FieldMode } from "../field-draft";
import { useReleaseCorrections } from "../hooks/use-release-corrections";
import type { ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { canBeEmpty, correctionFieldLabels, displayValue } from "../labels";
import styles from "./edit-release-dialog.module.css";
import { governanceFailureMessages } from "./governance-messages";

type EditReleaseDialogProps = {
  open: boolean;
  release: ReleaseTarget;
  commands: ReleaseCommands;
  onClose: () => void;
};

type FieldEditorProps = {
  field: CorrectionFieldDTO;
  draft: FieldDraft;
  error: string | null;
  disabled: boolean;
  onChange: (draft: FieldDraft) => void;
};

const modeLabels: Readonly<Record<FieldMode, string>> = { scan: "使用扫描值", custom: "自定义", empty: "留空" };

/** 一个字段：扫描值与当前显示、三选一与自定义输入。扫描结果不确定的字段多一个“确认扫描值”。 */
function FieldEditor({ field, draft, error, disabled, onChange }: FieldEditorProps) {
  const name = useId();
  const label = correctionFieldLabels[field.field];
  const modes: FieldMode[] = canBeEmpty(field.field) ? ["scan", "custom", "empty"] : ["scan", "custom"];
  // 已经有修正时不需要“确认”：扫描结论已经被人工结果取代。
  const canConfirm = field.uncertain && field.correction === null && draft.mode === "scan";
  const select = (mode: FieldMode) => onChange({ mode, text: draft.text });

  return (
    <fieldset className={styles.field} disabled={disabled}>
      <legend className={styles.legend}>
        {label}
        {field.correction === null ? null : (
          <StatusTag tone="accent" icon={<PencilLine size={12} />}>
            人工修正
          </StatusTag>
        )}
      </legend>
      <div className={styles.facts}>
        <p className={styles.fact}>
          <span className={styles.factLabel}>扫描值</span>
          <span className={styles.factValue}>{displayValue(field.scan_value)}</span>
          {field.uncertain ? (
            <StatusTag tone="outline" icon={<TriangleAlert size={12} />}>
              扫描结果不确定
            </StatusTag>
          ) : null}
        </p>
        {field.correction === null ? null : (
          <p className={styles.fact}>
            <span className={styles.factLabel}>当前显示</span>
            <span className={styles.factValue}>{displayValue(field.effective_value)}</span>
          </p>
        )}
      </div>
      <div className={styles.modes}>
        {modes.map((mode) => (
          <label key={mode} className={styles.mode}>
            <input type="radio" name={name} checked={draft.mode === mode} onChange={() => select(mode)} />
            {modeLabels[mode]}
          </label>
        ))}
      </div>
      {draft.mode === "custom" ? (
        <TextField
          label={`${label}（自定义）`}
          value={draft.text}
          error={error}
          autoComplete="off"
          {...(field.field === "year" ? { inputMode: "numeric" as const } : {})}
          onChange={(event) => onChange({ mode: "custom", text: event.target.value })}
        />
      ) : error === null ? null : (
        <p role="alert">{error}</p>
      )}
      {canConfirm ? (
        <div className={styles.confirm}>
          <Button onClick={() => onChange(confirmScanDraft(field))}>
            {field.scan_value === null ? "确认为空" : "确认扫描值"}
          </Button>
        </div>
      ) : null}
    </fieldset>
  );
}

function summaryOf(changedFields: readonly CorrectableField[], errorCount: number): string {
  if (errorCount > 0) return `有 ${errorCount} 个字段需要改好才能保存。`;
  if (changedFields.length === 0) return "还没有修改任何字段。";
  return `将修改：${changedFields.map((field) => correctionFieldLabels[field]).join("、")}。`;
}

type EditFieldsProps = {
  titleId: string;
  release: ReleaseTarget;
  state: CorrectionStateDTO;
  commands: ReleaseCommands;
  onClose: () => void;
};

function EditFields({ release, state, commands, onClose }: EditFieldsProps) {
  const summaryId = useId();
  const [edits, setEdits] = useState<DraftEdits>({});
  const planned = useMemo(() => planChanges(state, edits), [state, edits]);
  const errorCount = Object.keys(planned.errors).length;
  const commandState = commands.state;
  const pending = commandState.status === "pending" && commandState.command.kind === "correct";
  const failed = commandState.status === "failed" && commandState.command.kind === "correct" && commandState.command.release.id === release.id ? commandState : null;
  const canSave = planned.fields.length > 0 && errorCount === 0;

  const edit = (name: CorrectableField, draft: FieldDraft) => setEdits((current) => ({ ...current, [name]: draft }));
  const save = () => {
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    commands.submit({ kind: "correct", release, revision: state.revision, changes: planned.changes, fields: planned.fields }, { onSucceeded: onClose });
  };

  return (
    <>
      <div className={styles.fields}>
        {state.fields.map((field) => (
          <FieldEditor
            key={field.field}
            field={field}
            draft={draftOf(field, edits)}
            error={planned.errors[field.field] ?? null}
            disabled={pending}
            onChange={(draft) => edit(field.field, draft)}
          />
        ))}
      </div>
      <div className={styles.footer}>
        {failed === null ? null : failed.uncertain ? (
          <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="结果未确认" />
        ) : isHttpFailure(failed.failure, 409, "revision_conflict") ? (
          <Notice tone="warning" title="这张专辑刚被修改过">
            已重新载入，你的输入还在，请核对后再保存。
          </Notice>
        ) : (
          <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="没有保存" />
        )}
        <p id={summaryId} className={styles.summary} aria-live="polite">
          {summaryOf(planned.fields, errorCount)}
        </p>
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
          <Button
            variant="primary"
            busy={pending}
            disabled={!canSave && !failed?.uncertain}
            aria-describedby={summaryId}
            onClick={save}
          >
            {pending ? "正在保存…" : failed?.uncertain ? "重试保存" : "保存修改"}
          </Button>
        </div>
      </div>
    </>
  );
}

type EditReleaseFormProps = { titleId: string; headingRef: RefObject<HTMLHeadingElement | null>; release: ReleaseTarget; commands: ReleaseCommands; onClose: () => void };

function EditReleaseForm({ titleId, headingRef, release, commands, onClose }: EditReleaseFormProps) {
  const corrections = useReleaseCorrections(release.id);
  const gone = corrections.isError && isHttpFailure(failureOf(corrections.error), 404);
  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        编辑发行信息
      </h2>
      {corrections.data !== undefined ? (
        <EditFields titleId={titleId} release={release} state={corrections.data} commands={commands} onClose={onClose} />
      ) : (
        <>
          {corrections.isError ? (
            <FailureNotice
              error={corrections.error}
              messages={governanceFailureMessages}
              action={gone ? undefined : <Button onClick={() => void corrections.refetch()}>重试</Button>}
            />
          ) : (
            <Skeleton label="正在读取发行信息" lines={6} />
          )}
          <div className={styles.actions}>
            <Button onClick={onClose}>关闭</Button>
          </div>
        </>
      )}
    </div>
  );
}

/**
 * 编辑发行信息（管理员）：八个字段各自三选一——使用扫描值、自定义、留空（标题与专辑艺人不能留空）。
 * 只提交动过且确有变化的字段，带上读取时的人工修改版本号。409 时对话框保持打开、草稿保留并重新载入最新状态；
 * 结果不确定时“保存”变成用同一个幂等键“重试保存”；成功后对话框关闭，页面上的提示给出“撤销”。
 */
export function EditReleaseDialog({ open, release, commands, onClose }: EditReleaseDialogProps) {
  const titleId = useId();
  // 初始焦点在标题上：读取状态时对话框里没有别的稳定的可聚焦元素，内容换成字段之后焦点也不会丢。
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "correct";
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      initialFocus={headingRef}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending) onClose();
      }}
    >
      <EditReleaseForm titleId={titleId} headingRef={headingRef} release={release} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}
