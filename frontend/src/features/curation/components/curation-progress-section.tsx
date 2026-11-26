import { Link } from "@tanstack/react-router";
import { AudioWaveform, Disc3, FileText, Image, UserRound } from "lucide-react";
import type { ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
import type { CurationProgressDTO, CurationQueueDTO, CurationSettingsDTO } from "../api/decoders";
import { programStateText, queueNames, queueText } from "../curation-text";
import { useCurationClassifications, useCurationProgress } from "../hooks/use-curation";
import { EvidenceProviderRow } from "./evidence-provider-row";
import styles from "./curation-sections.module.css";

/** 已处理的比例：没有发行时为 0。 */
export function processedRatio(progress: CurationProgressDTO): number {
  return progress.releases.total === 0 ? 0 : Math.min(1, progress.releases.processed / progress.releases.total);
}

const queueIcons: Record<CurationQueueDTO["id"], ReactNode> = {
  release: <Disc3 size={16} aria-hidden="true" />,
  artwork: <Image size={16} aria-hidden="true" />,
  lyrics: <FileText size={16} aria-hidden="true" />,
  artist_media: <UserRound size={16} aria-hidden="true" />,
  fingerprint: <AudioWaveform size={16} aria-hidden="true" />,
};

/**
 * “进度”卡片：后台的状态、已处理与总数（进度条的数值来自服务器）、各类结果的计数、五条队列（整理链 B）、本地分类、
 * 依据库内或网页写入的修改与当前批次；拿不准的待定项写在“模型判断”里。
 * 从未打开过时收成一行；后台工作时每 10 秒刷新（见 progressRefetchInterval）。
 */
export function CurationProgressSection({ settings, refresh }: { settings: CurationSettingsDTO | undefined; refresh: () => void }) {
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
            <ProgressRows progress={data} refresh={refresh} />
          )
        }
      </QueryContent>
    </SettingsGroup>
  );
}

function ProgressRows({ progress, refresh }: { progress: CurationProgressDTO; refresh: () => void }) {
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
          已匹配 {formatCount(releases.matched)} · 未匹配 {formatCount(releases.unmatched)} · 有冲突 {formatCount(releases.conflict)}
          {releases.try_later > 0 ? ` · 等待重试 ${formatCount(releases.try_later)}` : ""}
        </span>
      </SettingRow>
      {progress.state === "unavailable"
        ? null
        : progress.queues.map((queue) => (
            <SettingRow
              key={queue.id}
              label={
                <span className={styles.iconLabel} title={`${queueNames[queue.id]}队列`}>
                  {queueIcons[queue.id]}
                  {queueNames[queue.id]}
                </span>
              }
              plainLabel
            >
              <span className={styles.value} aria-label={`${queueNames[queue.id]}队列：${queueText(queue, formatDateTime)}`}>
                {queueText(queue, formatDateTime)}
              </span>
            </SettingRow>
          ))}
      <ClassificationRow />
      <EvidenceProviderRow refresh={refresh} />
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

/** 本地分类（整理链 A）：可见发行按信息完整程度的四类计数，决定排队的先后。读不到时不显示这一行。 */
function ClassificationRow() {
  const classifications = useCurationClassifications();
  const data = classifications.data;
  if (data === undefined) return null;
  const { releases } = data;
  return (
    <SettingRow label="本地分类" description="按标签的完整程度排队：带外部编号的先查，信息不足的只在有声纹时识别。" plainLabel>
      <span className={styles.value}>
        带编号 {formatCount(releases.tagged_id)} · 有缺口 {formatCount(releases.has_gaps)} · 本地完整 {formatCount(releases.local_complete)} · 信息不足 {formatCount(releases.insufficient)}
      </span>
    </SettingRow>
  );
}
