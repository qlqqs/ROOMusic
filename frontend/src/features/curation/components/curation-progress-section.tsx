import { Link } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
import type { CurationProgressDTO, CurationSettingsDTO } from "../api/decoders";
import { programStateText } from "../curation-text";
import { useCurationProgress } from "../hooks/use-curation";
import styles from "./curation-sections.module.css";

/** 已处理的比例：没有发行时为 0。 */
export function processedRatio(progress: CurationProgressDTO): number {
  return progress.releases.total === 0 ? 0 : Math.min(1, progress.releases.processed / progress.releases.total);
}

/**
 * “进度”卡片：后台的状态、已处理与总数（进度条的数值来自服务器）、各类结果的计数、拿不准的待定项与当前批次。
 * 从未打开过时收成一行；后台工作时每 10 秒刷新（见 progressRefetchInterval）。
 */
export function CurationProgressSection({ settings }: { settings: CurationSettingsDTO | undefined }) {
  const progress = useCurationProgress(settings);
  const neverEnabled = settings !== undefined && !settings.enabled && settings.enabled_at === null;
  return (
    <SettingsGroup title="进度">
      <QueryContent query={progress} loading={<Skeleton label="正在加载进度" lines={3} />}>
        {(data) =>
          neverEnabled ? (
            <SettingRow label="状态" plainLabel>
              <span className={styles.value}>未打开 · 曲库共 {formatCount(data.releases.total)} 个发行</span>
            </SettingRow>
          ) : (
            <ProgressRows progress={data} />
          )
        }
      </QueryContent>
    </SettingsGroup>
  );
}

function ProgressRows({ progress }: { progress: CurationProgressDTO }) {
  const { releases } = progress;
  const ratio = processedRatio(progress);
  const state = programStateText[progress.state];
  const finished = progress.state === "idle" && progress.last_pass_finished_at !== null ? `，上一轮完成于 ${formatDateTime(progress.last_pass_finished_at)}` : "";
  return (
    <>
      <SettingRow label="状态" plainLabel>
        <span className={styles.value}>
          {state}
          {finished}
        </span>
      </SettingRow>
      <SettingRow label="已处理" plainLabel>
        <span className={styles.meterValue}>
          <span
            className={styles.meter}
            role="progressbar"
            aria-label="已处理的发行"
            aria-valuemin={0}
            aria-valuemax={releases.total}
            aria-valuenow={releases.processed}
          >
            <span className={styles.meterFill} style={{ inlineSize: `${(ratio * 100).toFixed(1)}%` }} />
          </span>
          <span className={styles.value}>
            {formatCount(releases.processed)} / {formatCount(releases.total)}
          </span>
        </span>
      </SettingRow>
      <SettingRow label="结果" plainLabel>
        <span className={styles.value}>
          对上 {formatCount(releases.matched)} · 拿不准 {formatCount(releases.uncertain)} · 没对上 {formatCount(releases.no_match)}
          {releases.try_later > 0 ? ` · 稍后再试 ${formatCount(releases.try_later)}` : ""}
        </span>
      </SettingRow>
      {progress.pending.waiting_model > 0 ? (
        <SettingRow label="待定" description="程序拿不准的，暂时留着；以后由模型判断或交给你处理。" plainLabel>
          <span className={styles.value}>{formatCount(progress.pending.waiting_model)} 项</span>
        </SettingRow>
      ) : null}
      {progress.batch === null ? null : (
        <SettingRow label="当前批次" plainLabel>
          <span className={styles.value}>
            {formatDateTime(progress.batch.opened_at)} 开始，已写 {formatCount(progress.batch.operations)} 条 ·{" "}
            <Link to="/settings/operations" search={{ batch: progress.batch.id }}>
              查看改动
            </Link>
          </span>
        </SettingRow>
      )}
    </>
  );
}
