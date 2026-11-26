import { Link } from "@tanstack/react-router";
import { Check, CircleAlert, RefreshCw } from "lucide-react";
import { useRef, useState, type KeyboardEvent } from "react";
import { flushSync } from "react-dom";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, ConfirmDialog, FailureNotice, QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import type { CurationModelProgressDTO, CurationSettingsDTO } from "../api/decoders";
import { modelStateText, modelUnavailableText, needsAgentSettings, tokenText } from "../curation-text";
import { useCurationConversations, useCurationProgress, useRepatrol, useUpdateCurationSettings, type CurationSettingsChange } from "../hooks/use-curation";
import styles from "./curation-sections.module.css";

const AGENT_SETTINGS = "/settings/agent";
const MAX_MODEL_BYTES = 200;
const WAN = 10_000;
const MAX_LIMIT_TOKENS = 1_000_000_000_000;

/**
 * “模型判断”卡片（M7 D、E2，整理链 G）：模型层的状态与原因、整理用的模型、每日 token 上限与联网兜底的上限、今天的用量（含联网）、
 * 待定项各状态的数目、重新巡检、最近的后台会话。
 * 模型与上限即改即存（照备份的“自动清理”：离开或回车时保存，勾“不限”立即保存），失败回到服务器的值并就近提示。
 * mine：当前管理员就是开启者（原因说的是他自己的管家设置，可以链接过去）；sameReviewModel：他的审查模型与有效的整理模型相同（U11）。
 */
export function CurationModelSection({ settings, mine, sameReviewModel }: { settings: CurationSettingsDTO | undefined; mine: boolean; sameReviewModel: boolean }) {
  const progress = useCurationProgress(settings);
  const [savedText, setSavedText] = useState("");
  const neverEnabled = settings !== undefined && !settings.enabled && settings.enabled_at === null;
  return (
    <SettingsGroup
      title="模型判断"
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
      {neverEnabled ? (
        <p className={styles.groupNote}>打开总开关、并在 Agent 设置里配置好模型接口之后，程序定不了的交给模型；有佐证或通过曲目结构核对的自动写入，其余记为未采用的建议。</p>
      ) : (
        <QueryContent query={progress} loading={<Skeleton label="正在加载模型判断" lines={4} />}>
          {(data) =>
            settings === undefined ? null : (
              <ModelRows model={data.model} settings={settings} mine={mine} sameReviewModel={sameReviewModel} onSaved={setSavedText} />
            )
          }
        </QueryContent>
      )}
    </SettingsGroup>
  );
}

function ModelRows({
  model,
  settings,
  mine,
  sameReviewModel,
  onSaved,
}: {
  model: CurationModelProgressDTO;
  settings: CurationSettingsDTO;
  mine: boolean;
  sameReviewModel: boolean;
  onSaved: (text: string) => void;
}) {
  const reason = model.unavailable_reason;
  const items = model.items;
  const total = model.today.input_tokens + model.today.output_tokens;
  const limit = model.daily_token_limit;
  const ratio = limit === null ? 0 : Math.min(1, total / limit);
  // 整理链 E 起不再有等人处理的项（waiting_human 固定为 0），这里不列。
  const counts = [
    ["等模型", items.waiting_model],
    ["处理中", items.in_progress],
    ["搁置", items.shelved],
  ] as const;
  const web = model.web_today.input_tokens + model.web_today.output_tokens;
  const showWeb = web > 0 || model.web_daily_token_limit !== null;
  const todayNote =
    model.state === "limit_reached"
      ? "到了上限，明天继续。"
      : model.web_limit_reached
        ? `联网到了上限，其它判断照常${model.web_retry_at === null ? "" : `，${formatDateTime(model.web_retry_at)} 恢复联网`}。`
        : `按服务器的日期（${model.today.date}）统计，含审查与联网。`;
  return (
    <>
      <SettingRow label="状态" plainLabel>
        <span className={styles.value}>
          {modelStateText[model.state]}
          {model.state === "unavailable" && reason !== null ? `：${modelUnavailableText(reason, mine)}` : ""}
          {mine && needsAgentSettings(reason) ? (
            <>
              {" "}
              <Link to={AGENT_SETTINGS}>去 Agent 设置</Link>
            </>
          ) : null}
        </span>
      </SettingRow>
      <ModelField settings={settings} sameReviewModel={sameReviewModel} onSaved={onSaved} />
      <LimitField settings={settings} field="daily_token_limit" onSaved={onSaved} />
      <LimitField settings={settings} field="web_daily_token_limit" onSaved={onSaved} />
      <SettingRow label="今天" description={todayNote} plainLabel>
        <span className={styles.meterValue}>
          {limit === null ? null : (
            <span className={styles.meter} role="progressbar" aria-label="今天的用量" aria-valuemin={0} aria-valuemax={limit} aria-valuenow={Math.min(total, limit)}>
              <span className={styles.meterFill} style={{ inlineSize: `${(ratio * 100).toFixed(1)}%` }} />
            </span>
          )}
          <span className={styles.value}>
            输入 {formatCount(model.today.input_tokens)} · 输出 {formatCount(model.today.output_tokens)}，共 {formatCount(total)}
            {showWeb ? `，其中联网 ${formatCount(web)}` : ""}
          </span>
        </span>
      </SettingRow>
      <SettingRow label="待定项" plainLabel>
        <span className={styles.value}>{counts.every(([, n]) => n === 0) ? "没有" : counts.map(([label, n]) => `${label} ${formatCount(n)}`).join(" · ")}</span>
      </SettingRow>
      <RepatrolRow />
      <RecentConversations />
    </>
  );
}

/** 整理用的模型：空串表示用开启者的对话模型；离开或回车时保存（去掉首尾空白，至多 200 字节）。 */
function ModelField({ settings, sameReviewModel, onSaved }: { settings: CurationSettingsDTO; sameReviewModel: boolean; onSaved: (text: string) => void }) {
  const update = useUpdateCurationSettings();
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const value = draft ?? settings.model;
  const commit = () => {
    if (draft === null) return;
    const next = draft.trim();
    if (utf8ByteLength(next) > MAX_MODEL_BYTES) {
      setError("模型名太长了（至多 200 字节）。");
      return;
    }
    setError(null);
    if (next === settings.model) {
      setDraft(null);
      return;
    }
    save(update, { model: next }, onSaved, () => setDraft(null));
  };
  const description = sameReviewModel ? (
    <>
      审查模型与它相同，审查的独立性有限；可以在 <Link to={AGENT_SETTINGS}>Agent 设置</Link>里另选一个审查模型。
    </>
  ) : (
    "空着就用开启者的对话模型。"
  );
  return (
    <SettingRow label="整理用的模型" description={description} plainLabel>
      {(ids) => (
        <div className={styles.field}>
          <input
            id={ids.controlId}
            className={styles.input}
            aria-label="整理用的模型"
            aria-describedby={ids.descriptionId}
            aria-invalid={error === null ? undefined : true}
            spellCheck={false}
            autoComplete="off"
            placeholder="与对话模型相同"
            value={value}
            onChange={(event) => {
              setDraft(event.target.value);
              setError(null);
            }}
            onBlur={commit}
            onKeyDown={(event) => onEnter(event, commit)}
          />
          <FieldError error={error} />
          {update.isError ? <FailureNotice error={update.error} context="write" /> : null}
        </div>
      )}
    </SettingRow>
  );
}

type LimitKey = "daily_token_limit" | "web_daily_token_limit";

const limitLabels: Record<LimitKey, string> = { daily_token_limit: "每日上限", web_daily_token_limit: "联网每日上限" };

function limitDescription(field: LimitKey, limit: number | null): string {
  if (field === "web_daily_token_limit") {
    return limit === null ? "不限：联网兜底只受每日上限约束。" : `联网兜底每天 ${tokenText(limit)} token，用量同时计入每日上限；达到后只停联网。`;
  }
  return limit === null ? "不限：后台整理不按天停下。" : `每天 ${tokenText(limit)} token，达到后当天不再启动新的判断。`;
}

/**
 * 每日上限与联网每日上限（field 选哪一个）：单位万 token（服务器的值不是整万时改用 token，编辑时不四舍五入）；勾“不限”立即保存，
 * 取消“不限”只解除停用并聚焦数字框，离开或回车时保存。
 */
function LimitField({ settings, field, onSaved }: { settings: CurationSettingsDTO; field: LimitKey; onSaved: (text: string) => void }) {
  const update = useUpdateCurationSettings();
  const limit = settings[field];
  const label = limitLabels[field];
  const inWan = limit === null || limit % WAN === 0;
  const unit = inWan ? WAN : 1;
  const max = MAX_LIMIT_TOKENS / unit;
  const serverText = limit === null ? String(1000) : String(limit / unit);
  const [draft, setDraft] = useState<{ value: string; unlimited: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const form = draft ?? { value: serverText, unlimited: limit === null };
  const reset = () => setDraft(null);

  const commit = (next: { value: string; unlimited: boolean }) => {
    if (next.unlimited) {
      setError(null);
      if (limit === null) return reset();
      return save(update, { [field]: null }, onSaved, reset);
    }
    const text = next.value.trim();
    const number = /^\d+$/.test(text) ? Number(text) : Number.NaN;
    if (!(number >= 1 && number <= max)) {
      setError(`填 1 到 ${formatCount(max)} 之间的整数。`);
      return;
    }
    setError(null);
    const tokens = number * unit;
    if (tokens === limit) return reset();
    save(update, { [field]: tokens }, onSaved, reset);
  };

  return (
    <SettingRow label={label} description={limitDescription(field, limit)} plainLabel>
      {(ids) => (
        <div role="group" aria-labelledby={ids.labelId} className={styles.field}>
          <div className={styles.limitRow}>
            <input
              ref={inputRef}
              id={ids.controlId}
              type="number"
              inputMode="numeric"
              min={1}
              max={max}
              step={1}
              className={styles.limitInput}
              aria-label={label}
              aria-invalid={error === null || form.unlimited ? undefined : true}
              disabled={form.unlimited}
              value={form.value}
              onChange={(event) => {
                setDraft({ ...form, value: event.target.value });
                setError(null);
              }}
              onBlur={() => {
                if (draft !== null) commit(draft);
              }}
              onKeyDown={(event) => onEnter(event, () => draft !== null && commit(draft))}
            />
            <span className={styles.unit} aria-hidden="true">
              {inWan ? "万 token" : "token"}
            </span>
            <label className={styles.check}>
              <input
                type="checkbox"
                checked={form.unlimited}
                onChange={(event) => {
                  const unlimited = event.target.checked;
                  const next = { ...form, unlimited };
                  flushSync(() => setDraft(next));
                  if (unlimited) commit(next);
                  else inputRef.current?.focus();
                }}
              />
              <span>不限</span>
            </label>
          </div>
          <FieldError error={form.unlimited ? null : error} />
          {update.isError ? <FailureNotice error={update.error} context="write" /> : null}
        </div>
      )}
    </SettingRow>
  );
}

/**
 * 重新巡检（整理链 D）：所有稳定的艺人名与专辑名重新交给模型检查一遍；更换模型或提示词升级时默认不重做，需要时在这里手动发起。
 * 行尾只有一个图标按钮，确认后提交；巡检排在模型队列最前，消耗模型用量。
 */
function RepatrolRow() {
  const repatrol = useRepatrol();
  const [open, setOpen] = useState(false);
  const label = "重新巡检全部名字";
  return (
    <SettingRow label="名字巡检" description={repatrol.isSuccess ? "已开始重新巡检。" : "检查艺人名与专辑名的别名和错字；只看新出现的名字。"} plainLabel>
      <>
        <Button variant="icon" icon={<RefreshCw size={16} />} label={label} onClick={() => setOpen(true)} disabled={repatrol.isPending} />
        <ConfirmDialog
          open={open}
          title="重新巡检全部名字？"
          confirmLabel="重新巡检"
          pending={repatrol.isPending}
          error={repatrol.isError ? <FailureNotice error={repatrol.error} context="write" /> : undefined}
          onCancel={() => {
            setOpen(false);
            repatrol.reset();
          }}
          onConfirm={() => repatrol.mutate(undefined, { onSuccess: () => setOpen(false) })}
        >
          <p>所有艺人名与专辑名会重新交给模型检查一遍，会消耗模型用量。已有的巡检结果在新结果出来之前继续使用。</p>
        </ConfirmDialog>
      </>
    </SettingRow>
  );
}

/** 最近的后台会话（当前管理员名下）：每个一个链接到管家的会话页；没有时不显示这一行。 */
function RecentConversations() {
  const conversations = useCurationConversations();
  const data = conversations.data;
  if (data === undefined || data.items.length === 0) return null;
  return (
    <SettingRow
      label="后台的会话"
      description={data.pagination.total > data.items.length ? `共 ${formatCount(data.pagination.total)} 个，这里列出最近的 ${formatCount(data.items.length)} 个。` : "点开可以看模型查了什么、为什么这么判断。"}
      plainLabel
    >
      <ul className={styles.linkList}>
        {data.items.map((item) => (
          <li key={item.id}>
            <Link to="/steward/$conversationId" params={{ conversationId: item.id }}>
              {item.title === "" ? "自动整理" : item.title}
            </Link>
            <span className={styles.meta}> · {formatDateTime(item.updated_at)}</span>
          </li>
        ))}
      </ul>
    </SettingRow>
  );
}

function FieldError({ error }: { error: string | null }) {
  return error === null ? null : (
    <p className={styles.error}>
      <CircleAlert size={14} aria-hidden="true" />
      <span>{error}</span>
    </p>
  );
}

function onEnter(event: KeyboardEvent<HTMLInputElement>, commit: () => void) {
  if (event.key !== "Enter") return;
  event.preventDefault();
  commit();
}

/** 提交一项设置：成功写“已保存。”，失败时清掉草稿回到服务器的值（错误由调用方在字段下显示）。 */
function save(update: ReturnType<typeof useUpdateCurationSettings>, change: CurationSettingsChange, onSaved: (text: string) => void, reset: () => void) {
  onSaved("");
  update.mutate(change, {
    onSuccess: () => {
      reset();
      onSaved("已保存。");
    },
    onError: reset,
  });
}
