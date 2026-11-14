import { Check, CircleAlert } from "lucide-react";
import { useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { flushSync } from "react-dom";
import { ConfirmDialog, FailureNotice, Notice, QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
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

const sameRule = (a: BackupRetentionDTO, b: BackupRetentionDTO) => a.keep_days === b.keep_days && a.keep_count === b.keep_count;
const sameDraft = (a: Draft, b: Draft) => a.days === b.days && a.daysUnlimited === b.daysUnlimited && a.count === b.count && a.countUnlimited === b.countUnlimited;

function parseLimit(value: string, range: { min: number; max: number }): number | null {
  if (!/^\d+$/.test(value.trim())) return null;
  const number = Number(value.trim());
  return number >= range.min && number <= range.max ? number : null;
}

/**
 * 备份页的“自动清理”卡片：保留天数与保留份数各一行，每项可以勾“不限”。即改即存，和播放设置一样没有“保存”按钮：
 * 勾上“不限”就保存，数字框（含取消“不限”之后）在离开或按回车时保存；保存后标题旁写“已保存”。数据来自与其他区段相同的查询；表单没改过时跟着服务器的值，改过就不覆盖。
 * 保存先不带确认提交：会删除旧备份时服务器什么都不改、只回答份数，这时弹确认框，确认后再带确认提交，取消则恢复原值。保存失败也恢复原值并显示错误。
 */
export function BackupRetentionSection() {
  const overview = useBackupOverview();
  const [savedText, setSavedText] = useState("");
  return (
    <SettingsGroup
      title="自动清理"
      status={
        <p role="status" className={styles.saved}>
          {savedText === "" ? null : (
            <>
              <Check size={14} aria-hidden="true" />
              {savedText}
            </>
          )}
        </p>
      }
    >
      <QueryContent query={overview} loading={<Skeleton label="正在加载自动清理设置" lines={2} />}>
        {(data) => <RetentionForm retention={data.retention} onSaved={setSavedText} />}
      </QueryContent>
    </SettingsGroup>
  );
}

/** onSaved：保存结果的一句话（“已保存。”），空串表示清掉。 */
function RetentionForm({ retention, onSaved }: { retention: BackupRetentionDTO | null; onSaved: (text: string) => void }) {
  const save = useSaveRetention();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [errors, setErrors] = useState<Errors>(noErrors);
  const [confirming, setConfirming] = useState<{ rule: BackupRetentionDTO; deletions: number } | null>(null);
  const [failedDeletions, setFailedDeletions] = useState(0);
  // 正在保存的规则（用 ref，同一轮事件里的回车与离开也能看到）与保存期间又改了的值：这一次保存结束后接着保存后者，不丢掉，也不被上一次的结果覆盖。
  const inflight = useRef<BackupRetentionDTO | null>(null);
  const queued = useRef<{ draft: Draft; rule: BackupRetentionDTO } | null>(null);
  const daysRef = useRef<HTMLInputElement>(null);
  const countRef = useRef<HTMLInputElement>(null);
  const form = draft ?? draftOf(retention);

  /** 检查一份草稿：有错就在字段旁显示（不抢焦点：离开数字框时才检查），返回 null。 */
  const validate = (next: Draft): BackupRetentionDTO | null => {
    const days = next.daysUnlimited ? null : parseLimit(next.days, RETENTION_DAYS_RANGE);
    const count = next.countUnlimited ? null : parseLimit(next.count, RETENTION_COUNT_RANGE);
    const found: Errors = {
      days: !next.daysUnlimited && days === null ? retentionDaysError : null,
      count: !next.countUnlimited && count === null ? retentionCountError : null,
    };
    setErrors(found);
    if (found.days !== null || found.count !== null) return null;
    return { keep_days: days, keep_count: count };
  };

  const saved = (deleted: number, failed: number) => {
    onSaved(retentionSavedText(deleted));
    setFailedDeletions(failed);
  };

  const submit = (next: Draft, rule: BackupRetentionDTO) => {
    onSaved("");
    setFailedDeletions(0);
    inflight.current = rule;
    save.mutate(
      { rule, confirm: false },
      {
        onSuccess: (response) => {
          inflight.current = null;
          if (response.data.confirmation_required) {
            queued.current = null;
            setConfirming({ rule, deletions: response.data.deletions });
            return;
          }
          saved(response.data.deleted, response.data.failed);
          const after = queued.current;
          queued.current = null;
          // 保存期间又在输入、还没提交的草稿留着，不被这次的结果冲掉。
          if (after === null) setDraft((current) => (current === next ? null : current));
          else submit(after.draft, after.rule);
        },
        onError: () => {
          inflight.current = null;
          queued.current = null;
          setDraft(null);
        },
      },
    );
  };

  /** 保存一份草稿：和服务器的值相同就不提交；正在等确认时不再提交；和正在保存（或已排队）的规则相同时不重复提交。 */
  const commit = (next: Draft) => {
    if (confirming !== null) return;
    const rule = validate(next);
    if (rule === null) return;
    if (inflight.current !== null) {
      if (!sameRule(rule, queued.current?.rule ?? inflight.current)) queued.current = { draft: next, rule };
      return;
    }
    if (retention !== null && sameRule(rule, retention)) {
      // 停用的数字框里还留着刚才输入的值时保留草稿，不把它换回服务器的值。
      if (sameDraft(next, draftOf(retention))) setDraft(null);
      return;
    }
    submit(next, rule);
  };

  const edit = (change: Partial<Draft>): Draft => {
    const next = { ...form, ...change };
    setDraft(next);
    return next;
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
          setDraft(null);
          saved(response.data.deleted, response.data.failed);
        },
      },
    );
  };

  /**
   * 勾上“不限”立即保存。取消勾选只解除停用并聚焦数字框，等离开或回车时再保存：
   * 框里还是旧值或默认值，马上保存可能按用户没打算的数字弹出“将删除 n 份”的确认。
   */
  const commitUnlimited = (next: Draft, unlimited: boolean) => {
    if (unlimited) commit(next);
  };

  /** 数字框离开或回车时保存；没有改过（没有草稿）时什么都不做。 */
  const commitTyped = () => {
    if (draft !== null) commit(draft);
  };
  const onEnter = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    commitTyped();
  };

  return (
    <>
      {retention === null ? <Notice tone="warning">{RETENTION_UNREADABLE_MESSAGE}</Notice> : null}
      {/* 两行在卡片里贴边，自己画行间的分隔线（设置行不是卡片的直接子元素）。 */}
      <div className={styles.form} data-settings-flush="">
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
          onCommit={commitTyped}
          onEnter={onEnter}
          onUnlimited={(daysUnlimited) => commitUnlimited(edit({ daysUnlimited }), daysUnlimited)}
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
          onCommit={commitTyped}
          onEnter={onEnter}
          onUnlimited={(countUnlimited) => commitUnlimited(edit({ countUnlimited }), countUnlimited)}
        />
      </div>
      {failedDeletions > 0 ? <Notice tone="warning">{retentionFailedText(failedDeletions)}</Notice> : null}
      {save.isError && confirming === null ? <FailureNotice error={save.error} context="write" messages={BACKUP_RETENTION_MESSAGES} /> : null}
      <ConfirmDialog
        open={confirming !== null}
        title="保存清理设置？"
        confirmLabel="保存并删除"
        pending={save.isPending}
        error={save.isError ? <FailureNotice error={save.error} context="write" messages={BACKUP_RETENTION_MESSAGES} /> : undefined}
        onConfirm={confirm}
        onCancel={() => {
          // 不保存：恢复原值。
          setConfirming(null);
          setDraft(null);
          setErrors(noErrors);
          save.reset();
        }}
      >
        <p>{confirming === null ? "" : retentionConfirmText(confirming.deletions)}</p>
      </ConfirmDialog>
    </>
  );
}

/**
 * 一项上限的设置行：名称在左，右侧是数字框、单位与“不限”（一组，以名称为组名）。勾“不限”时数字框停用但保留原值；取消勾选时焦点回到数字框。
 * 数字框离开或回车时保存，勾上“不限”就保存。
 * 错误在控件下方一行。
 */
function LimitField({
  legend,
  unit,
  range,
  value,
  unlimited,
  error,
  inputRef,
  onValue,
  onCommit,
  onEnter,
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
  /** 离开数字框时保存。 */
  onCommit: () => void;
  onEnter: (event: KeyboardEvent<HTMLInputElement>) => void;
  onUnlimited: (unlimited: boolean) => void;
}) {
  return (
    <SettingRow label={legend} plainLabel>
      {(ids) => {
        const errorId = `${ids.controlId}-error`;
        return (
          <div role="group" aria-labelledby={ids.labelId} className={styles.field}>
            <div className={styles.row}>
              <input
                ref={inputRef}
                id={ids.controlId}
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
                onBlur={onCommit}
                onKeyDown={onEnter}
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
          </div>
        );
      }}
    </SettingRow>
  );
}
