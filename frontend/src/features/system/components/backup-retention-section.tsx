import { CircleAlert } from "lucide-react";
import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { flushSync } from "react-dom";
import { Button, ConfirmDialog, FailureNotice, Notice, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { RETENTION_COUNT_RANGE, RETENTION_DAYS_RANGE, type BackupRetentionDTO } from "../api/decoders";
import { useBackupOverview } from "../hooks/use-backup-overview";
import { useSaveRetention } from "../hooks/use-save-retention";
import {
  BACKUP_RETENTION_MESSAGES,
  RETENTION_UNREADABLE_MESSAGE,
  retentionConfirmText,
  retentionCountError,
  retentionDaysError,
  retentionFailedText,
  retentionSavedText,
} from "./backup-messages";
import styles from "./backup-retention-section.module.css";

// 服务器读不到规则时表单用的默认值（与从未保存过时的默认一致）；勾着“不限”的那一项，数字框里放这些值。
const DEFAULT_DAYS = 14;
const DEFAULT_COUNT = 10;

type Draft = { days: string; daysUnlimited: boolean; count: string; countUnlimited: boolean };
type Errors = { days: string | null; count: string | null };

const noErrors: Errors = { days: null, count: null };

function draftOf(retention: BackupRetentionDTO | null): Draft {
  const days = retention === null ? DEFAULT_DAYS : retention.keep_days;
  const count = retention === null ? null : retention.keep_count;
  return { days: String(days ?? DEFAULT_DAYS), daysUnlimited: days === null, count: String(count ?? DEFAULT_COUNT), countUnlimited: count === null };
}

function parseLimit(value: string, range: { min: number; max: number }): number | null {
  if (!/^\d+$/.test(value.trim())) return null;
  const number = Number(value.trim());
  return number >= range.min && number <= range.max ? number : null;
}

/**
 * 备份页的“自动清理”：保留天数与保留份数，每项可以勾“不限”。数据来自与其他区段相同的查询；表单没改过时跟着服务器的值，改过就不覆盖。
 * 保存先不带确认提交：会删除旧备份时服务器什么都不改、只回答份数，这时弹确认框，确认后再带确认提交。结果就近显示在按钮旁。
 */
export function BackupRetentionSection() {
  const overview = useBackupOverview();
  return (
    <Panel title="自动清理">
      <QueryContent query={overview} loading={<Skeleton label="正在加载自动清理设置" lines={2} />}>
        {(data) => <RetentionForm retention={data.retention} />}
      </QueryContent>
    </Panel>
  );
}

function RetentionForm({ retention }: { retention: BackupRetentionDTO | null }) {
  const save = useSaveRetention();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [errors, setErrors] = useState<Errors>(noErrors);
  const [confirming, setConfirming] = useState<{ rule: BackupRetentionDTO; deletions: number } | null>(null);
  const [result, setResult] = useState<{ deleted: number; failed: number } | null>(null);
  const daysRef = useRef<HTMLInputElement>(null);
  const countRef = useRef<HTMLInputElement>(null);
  const form = draft ?? draftOf(retention);

  const edit = (change: Partial<Draft>) => {
    setDraft({ ...form, ...change });
    setResult(null);
  };

  const saved = (deleted: number, failed: number) => {
    setDraft(null);
    setResult({ deleted, failed });
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (save.isPending) return;
    const days = form.daysUnlimited ? null : parseLimit(form.days, RETENTION_DAYS_RANGE);
    const count = form.countUnlimited ? null : parseLimit(form.count, RETENTION_COUNT_RANGE);
    const next: Errors = {
      days: !form.daysUnlimited && days === null ? retentionDaysError : null,
      count: !form.countUnlimited && count === null ? retentionCountError : null,
    };
    setErrors(next);
    if (next.days !== null) daysRef.current?.focus();
    else if (next.count !== null) countRef.current?.focus();
    if (next.days !== null || next.count !== null) return;
    const rule = { keep_days: days, keep_count: count };
    setResult(null);
    save.mutate(
      { rule, confirm: false },
      {
        onSuccess: (response) => {
          if (response.data.confirmation_required) setConfirming({ rule, deletions: response.data.deletions });
          else saved(response.data.deleted, response.data.failed);
        },
      },
    );
  };

  const confirm = () => {
    if (confirming === null) return;
    save.mutate(
      { rule: confirming.rule, confirm: true },
      {
        onSuccess: (response) => {
          // 带确认的保存总会保存；这里只为类型收窄。
          if (response.data.confirmation_required) return;
          setConfirming(null);
          saved(response.data.deleted, response.data.failed);
        },
      },
    );
  };

  return (
    <>
      {retention === null ? <Notice tone="warning">{RETENTION_UNREADABLE_MESSAGE}</Notice> : null}
      <form className={styles.form} noValidate onSubmit={submit}>
        <div className={styles.fields}>
          <LimitField
            legend="保留天数"
            unit="天"
            range={RETENTION_DAYS_RANGE}
            value={form.days}
            unlimited={form.daysUnlimited}
            error={errors.days}
            inputRef={daysRef}
            onValue={(days) => {
              edit({ days });
              setErrors((previous) => ({ ...previous, days: null }));
            }}
            onUnlimited={(daysUnlimited) => edit({ daysUnlimited })}
          />
          <LimitField
            legend="保留份数"
            unit="份"
            range={RETENTION_COUNT_RANGE}
            value={form.count}
            unlimited={form.countUnlimited}
            error={errors.count}
            inputRef={countRef}
            onValue={(count) => {
              edit({ count });
              setErrors((previous) => ({ ...previous, count: null }));
            }}
            onUnlimited={(countUnlimited) => edit({ countUnlimited })}
          />
        </div>
        <div className={styles.submit}>
          <Button type="submit" variant="primary" busy={save.isPending && confirming === null}>
            {save.isPending && confirming === null ? "正在保存…" : "保存"}
          </Button>
          <p role="status" className={styles.saved}>
            {result === null ? "" : retentionSavedText(result.deleted)}
          </p>
        </div>
        {result !== null && result.failed > 0 ? <Notice tone="warning">{retentionFailedText(result.failed)}</Notice> : null}
        {save.isError && confirming === null ? <FailureNotice error={save.error} context="write" messages={BACKUP_RETENTION_MESSAGES} /> : null}
      </form>
      <ConfirmDialog
        open={confirming !== null}
        title="保存清理设置？"
        confirmLabel="保存并删除"
        pending={save.isPending}
        error={save.isError ? <FailureNotice error={save.error} context="write" messages={BACKUP_RETENTION_MESSAGES} /> : undefined}
        onConfirm={confirm}
        onCancel={() => {
          setConfirming(null);
          save.reset();
        }}
      >
        <p>{confirming === null ? "" : retentionConfirmText(confirming.deletions)}</p>
      </ConfirmDialog>
    </>
  );
}

/** 一项上限：数字框、单位与“不限”。勾“不限”时数字框停用但保留原值；取消勾选时焦点回到数字框。 */
function LimitField({
  legend,
  unit,
  range,
  value,
  unlimited,
  error,
  inputRef,
  onValue,
  onUnlimited,
}: {
  legend: string;
  unit: string;
  range: { min: number; max: number };
  value: string;
  unlimited: boolean;
  error: string | null;
  inputRef: RefObject<HTMLInputElement | null>;
  onValue: (value: string) => void;
  onUnlimited: (unlimited: boolean) => void;
}) {
  const id = useId();
  const errorId = `${id}-error`;
  return (
    <fieldset className={styles.field}>
      <legend className={styles.legend}>{legend}</legend>
      <div className={styles.row}>
        <input
          ref={inputRef}
          type="number"
          inputMode="numeric"
          min={range.min}
          max={range.max}
          step={1}
          className={styles.input}
          aria-label={legend}
          aria-invalid={error === null || unlimited ? undefined : true}
          aria-describedby={error === null || unlimited ? undefined : errorId}
          disabled={unlimited}
          value={value}
          onChange={(event) => onValue(event.target.value)}
        />
        <span className={styles.unit} aria-hidden="true">
          {unit}
        </span>
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={unlimited}
            onChange={(event) => {
              const checked = event.target.checked;
              // 取消“不限”时数字框要先解除停用才能聚焦，所以同步提交这次更新。
              flushSync(() => onUnlimited(checked));
              if (!checked) inputRef.current?.focus();
            }}
          />
          <span>不限</span>
        </label>
      </div>
      {error === null || unlimited ? null : (
        <p id={errorId} className={styles.error}>
          <CircleAlert size={14} aria-hidden="true" />
          <span>{error}</span>
        </p>
      )}
    </fieldset>
  );
}
