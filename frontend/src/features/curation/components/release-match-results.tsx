import { RotateCcw } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, Disclosure, FailureNotice, useTooltip, VisuallyHidden } from "../../../shared/ui";
import { sourceName } from "../../governance";
import type { ReleaseMatchResultDTO } from "../api/decoders";
import { matchConflictText, matchStatusText } from "../curation-text";
import { useRecheckRelease, useReleaseMatchResults } from "../hooks/use-curation";
import styles from "./release-match-results.module.css";

/** 查询时间、冲突在哪、候选已撤销：写进悬停提示与读屏名，不写成句子。 */
function resultDetails(result: ReleaseMatchResultDTO): string {
  const parts: string[] = [];
  if (result.checked_at !== null) parts.push(`${formatDateTime(result.checked_at)} 查询`);
  if (result.reason === "candidates_revoked") parts.push("候选已撤销");
  if (result.conflicts.length > 0) parts.push(`冲突：${[...new Set(result.conflicts.map(matchConflictText))].join("、")}`);
  return parts.join(" · ");
}

/**
 * 专辑详情的管理员区块“外部识别”：每个来源一行——结论与平台名（未匹配 · MusicBrainz），查询时间、冲突在哪和“候选已撤销”在悬停提示与读屏名里。
 * 普通未匹配（不是候选已撤销）的行尾有重查按钮，只走程序。折叠时标题旁也写出结论。还没有任何来源的记录时不显示。
 */
export function ReleaseMatchResults({ releaseId }: { releaseId: string }) {
  const results = useReleaseMatchResults(releaseId);
  const items = results.data;
  if (items === undefined || items.length === 0) return null;
  const summary = items.map((item) => `${matchStatusText[item.status]} · ${sourceName(item.source)}`).join("；");
  return (
    <Disclosure title="外部识别" detail={`（${summary}）`}>
      <ul className={styles.list}>
        {items.map((item) => (
          <MatchResultRow key={item.source} releaseId={releaseId} result={item} />
        ))}
      </ul>
    </Disclosure>
  );
}

function MatchResultRow({ releaseId, result }: { releaseId: string; result: ReleaseMatchResultDTO }) {
  const details = resultDetails(result);
  const tip = useTooltip(details === "" ? undefined : details);
  const recheckable = result.status === "unmatched" && result.reason !== "candidates_revoked";
  return (
    <li className={styles.row}>
      <span className={styles.result} {...tip.triggerProps}>
        <span className={styles.status} data-status={result.status}>
          {matchStatusText[result.status]}
        </span>
        <span className={styles.platform}>{sourceName(result.source)}</span>
        {details === "" ? null : <VisuallyHidden>，{details}</VisuallyHidden>}
      </span>
      {tip.tooltip}
      {recheckable ? <RecheckButton releaseId={releaseId} /> : null}
    </li>
  );
}

/** 重查一次（只走程序）；受理后按钮停用并说明已排上，结果由后台写回。 */
function RecheckButton({ releaseId }: { releaseId: string }) {
  const recheck = useRecheckRelease(releaseId);
  return (
    <>
      <Button
        variant="icon"
        icon={<RotateCcw size={16} />}
        label={recheck.isSuccess ? "已排上重查" : "重新查询"}
        onClick={() => recheck.mutate()}
        disabled={recheck.isPending || recheck.isSuccess}
      />
      {recheck.isError ? <FailureNotice error={recheck.error} context="write" /> : null}
    </>
  );
}
