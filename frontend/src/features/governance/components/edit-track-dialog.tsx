import { PencilLine, TriangleAlert, Wand } from "lucide-react";
import { useId, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { failureOf, isHttpFailure, isOutcomeUncertain } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { Button, FailureNotice, ModalDialog, Notice, Skeleton, StatusTag, TextField } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import type { TrackCorrectableField, TrackCorrectionStateDTO } from "../api/decoders";
import { correctTrack, fetchTrackCorrections, type TrackCorrectionChange } from "../api/endpoints";
import { sourceName } from "../change-summary";
import { displayValue, trackFieldLabels } from "../labels";
import { governanceKeys } from "../query-keys";
import styles from "./edit-release-dialog.module.css";
import { governanceFailureMessages } from "./governance-messages";

type TrackField = TrackCorrectionStateDTO["fields"][number];
type Draft = { custom: boolean; text: string };

const trackMessages: Readonly<Record<string, string>> = {
  ...governanceFailureMessages,
  revision_conflict: "这首曲目刚被修改过，已重新载入，请核对后再保存。",
  no_changes: "和现在显示的一样，没有需要保存的修改。",
};

/** 字段的草稿：有人工修正时默认是“自定义”且填着修正值，否则是“使用扫描值（或自动补全的值）”。 */
function draftOf(field: TrackField, edits: Partial<Record<TrackCorrectableField, Draft>>): Draft {
  return edits[field.field] ?? { custom: field.correction !== null, text: field.correction?.value ?? field.effective_value ?? "" };
}

/** 要提交的修改：自定义且与当前修正不同 → set；原来有修正、改回“使用扫描值” → clear。自定义不能为空。 */
function planOf(state: TrackCorrectionStateDTO, edits: Partial<Record<TrackCorrectableField, Draft>>) {
  const changes: Partial<Record<TrackCorrectableField, TrackCorrectionChange>> = {};
  const errors: Partial<Record<TrackCorrectableField, string>> = {};
  for (const field of state.fields) {
    const draft = draftOf(field, edits);
    if (draft.custom) {
      const value = draft.text.trim();
      if (value === "") errors[field.field] = `${trackFieldLabels[field.field]}不能留空。`;
      else if (value !== field.correction?.value) changes[field.field] = { mode: "set", value };
    } else if (field.correction !== null) {
      changes[field.field] = { mode: "clear" };
    }
  }
  return { changes, errors, fields: Object.keys(changes) as TrackCorrectableField[] };
}

function FieldEditor({ field, draft, error, disabled, onChange }: { field: TrackField; draft: Draft; error: string | null; disabled: boolean; onChange: (draft: Draft) => void }) {
  const name = useId();
  const label = trackFieldLabels[field.field];
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
        {field.auto === null ? null : (
          <p className={styles.fact}>
            <span className={styles.factLabel}>自动补全</span>
            <span className={styles.factValue}>{field.auto.value}</span>
            <StatusTag tone="neutral" icon={<Wand size={12} />}>
              {sourceName(field.auto.provider)}
            </StatusTag>
          </p>
        )}
      </div>
      <div className={styles.modes}>
        <label className={styles.mode}>
          <input type="radio" name={name} checked={!draft.custom} onChange={() => onChange({ ...draft, custom: false })} />
          {field.auto === null ? "使用扫描值" : "使用自动补全的值"}
        </label>
        <label className={styles.mode}>
          <input type="radio" name={name} checked={draft.custom} onChange={() => onChange({ ...draft, custom: true })} />
          自定义
        </label>
      </div>
      {draft.custom ? (
        <TextField label={`${label}（自定义）`} value={draft.text} error={error} autoComplete="off" onChange={(event) => onChange({ custom: true, text: event.target.value })} />
      ) : null}
    </fieldset>
  );
}

function EditFields({ state, trackId, refresh, onClose }: { state: TrackCorrectionStateDTO; trackId: string; refresh: () => Promise<unknown> | void; onClose: () => void }) {
  const summaryId = useId();
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const [edits, setEdits] = useState<Partial<Record<TrackCorrectableField, Draft>>>({});
  const plan = useMemo(() => planOf(state, edits), [state, edits]);
  const errorCount = Object.keys(plan.errors).length;
  // 同一份修改的重试沿用同一个幂等键；修改内容变了就换新键。
  const key = useRef<{ body: string; key: string } | null>(null);
  const save = useMutation({
    mutationFn: () => {
      const body = JSON.stringify([state.revision, plan.changes]);
      if (key.current?.body !== body) key.current = { body, key: newIdempotencyKey() };
      return correctTrack(trackId, state.revision, plan.changes, key.current.key);
    },
    onSuccess: () => onClose(),
    onSettled: async () => {
      if (!scope.isCurrent()) return;
      await Promise.all([queryClient.invalidateQueries({ queryKey: governanceKeys.all(scope.generation) }), refresh()]);
    },
  });
  const failure = save.isError ? failureOf(save.error) : null;
  const uncertain = failure !== null && isOutcomeUncertain(failure);
  const summary =
    errorCount > 0 ? `有 ${errorCount} 个字段需要改好才能保存。` : plan.fields.length === 0 ? "还没有修改任何字段。" : `将修改：${plan.fields.map((field) => trackFieldLabels[field]).join("、")}。`;
  return (
    <>
      <div className={styles.fields}>
        {state.fields.map((field) => (
          <FieldEditor
            key={field.field}
            field={field}
            draft={draftOf(field, edits)}
            error={plan.errors[field.field] ?? null}
            disabled={save.isPending}
            onChange={(draft) => setEdits((current) => ({ ...current, [field.field]: draft }))}
          />
        ))}
      </div>
      <div className={styles.footer}>
        {save.isError ? (
          isHttpFailure(failure, 409, "revision_conflict") ? (
            <Notice tone="warning" title="这首曲目刚被修改过">
              已重新载入，你的输入还在，请核对后再保存。
            </Notice>
          ) : (
            <FailureNotice error={save.error} context="write" messages={trackMessages} title={uncertain ? "结果未确认" : "没有保存"} />
          )
        ) : null}
        <p id={summaryId} className={styles.summary} aria-live="polite">
          {summary}
        </p>
        <div className={styles.actions}>
          <Button onClick={onClose} disabled={save.isPending}>
            取消
          </Button>
          <Button variant="primary" busy={save.isPending} disabled={(plan.fields.length === 0 || errorCount > 0) && !uncertain} aria-describedby={summaryId} onClick={() => save.mutate()}>
            {save.isPending ? "正在保存…" : uncertain ? "重试保存" : "保存修改"}
          </Button>
        </div>
      </div>
    </>
  );
}

/**
 * 修正曲目信息（M7 I，管理员）：曲目标题与曲目艺人各自二选一——使用扫描值（有自动补全时是自动补全的值）或自定义；两个字段都不能留空。
 * 只提交确有变化的字段，带上这首曲目自己的人工修改版本号；409 时重新载入、保留输入；结果不确定时用同一个幂等键“重试保存”。
 * 打开时才读取状态（不缓存）；保存之后刷新治理数据并调 app 的刷新（专辑详情、搜索等）。
 */
export function EditTrackDialog({ trackId, trackTitle, refresh, onClose }: { trackId: string; trackTitle: string; refresh: () => Promise<unknown> | void; onClose: () => void }) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const { generation } = useViewerScope();
  const corrections = useQuery({
    queryKey: governanceKeys.trackCorrections(generation, trackId),
    queryFn: ({ signal }) => fetchTrackCorrections(trackId, signal),
    select: (response) => response.data,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const gone = corrections.isError && isHttpFailure(failureOf(corrections.error), 404);
  return (
    <ModalDialog open titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={onClose}>
      <div className={styles.content}>
        <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
          修正曲目信息：{trackTitle}
        </h2>
        {corrections.data !== undefined ? (
          <EditFields state={corrections.data} trackId={trackId} refresh={refresh} onClose={onClose} />
        ) : (
          <>
            {corrections.isError ? (
              <FailureNotice error={corrections.error} messages={trackMessages} action={gone ? undefined : <Button onClick={() => void corrections.refetch()}>重试</Button>} />
            ) : (
              <Skeleton label="正在读取曲目信息" lines={4} />
            )}
            <div className={styles.actions}>
              <Button onClick={onClose}>关闭</Button>
            </div>
          </>
        )}
      </div>
    </ModalDialog>
  );
}
