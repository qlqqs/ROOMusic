import { ArrowLeftRight, RotateCcw } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, Disclosure, FailureNotice, useTooltip, VisuallyHidden } from "../../../shared/ui";
import { correctionFieldLabels, sourceName, trackFieldLabels } from "../../governance";
import type { ReleaseMatchResultDTO } from "../api/decoders";
import { matchConflictText, matchStatusText } from "../curation-text";
import { useRecheckRelease, useReleaseMatchResults } from "../hooks/use-curation";
import { useAdoptMusicBrainzName } from "../hooks/use-match-decisions";
import { MatchCandidates } from "./match-candidates";
import styles from "./release-match-results.module.css";

/** 查询时间、冲突在哪、候选已撤销：写进悬停提示与读屏名，不写成句子。 */
function resultDetails(result: ReleaseMatchResultDTO): string {
  const parts: string[] = [];
  if (result.checked_at !== null) parts.push(`${formatDateTime(result.checked_at)} 查询`);
  if (result.reason === "candidates_revoked") parts.push("候选已撤销");
  if (result.conflicts.length > 0) parts.push(`冲突：${[...new Set(result.conflicts.map(matchConflictText))].join("、")}`);
  return parts.join(" · ");
}

/** 专辑详情里 MusicBrainz 的写法（只列和本地写法不同的）。 */
export type MusicBrainzName = {
  target_type: "release" | "track";
  target_id: string;
  field: string;
  local_value: string;
  effective_value: string;
  musicbrainz_value: string;
  basis_operation_id: string;
  revision: number;
  can_adopt: boolean;
};

/**
 * 专辑详情的管理员区块“外部识别”：每个来源一行——结论与平台名（未匹配 · MusicBrainz），查询时间、冲突在哪和“候选已撤销”在悬停提示与读屏名里。
 * 普通未匹配（不是候选已撤销）的行尾有重查按钮，只走程序；有冲突的列出几张都对得上的候选，点一下就能定（ADR 0009 第 6.3 节）。
 * 认到之后和本地写法不同的 MusicBrainz 写法列在下面，能采用时一键采用（自动层，可撤销）。折叠时标题旁也写出结论。还没有任何来源的记录时不显示。
 */
export function ReleaseMatchResults({ releaseId, names, refresh }: { releaseId: string; names: readonly MusicBrainzName[]; refresh: () => void }) {
  const results = useReleaseMatchResults(releaseId);
  const items = results.data;
  if (items === undefined || items.length === 0) return null;
  const summary = items.map((item) => `${matchStatusText[item.status]} · ${sourceName(item.source)}`).join("；");
  return (
    <Disclosure title="外部识别" detail={`（${summary}）`}>
      <ul className={styles.list}>
        {items.map((item) => (
          <MatchResultRow key={item.source} releaseId={releaseId} result={item} refresh={refresh} />
        ))}
      </ul>
      {names.length === 0 ? null : <MusicBrainzNames releaseId={releaseId} names={names} refresh={refresh} />}
    </Disclosure>
  );
}

/** 字段的说法：专辑上的字段用发行字段名，曲目上的加“曲目的”。 */
function nameFieldLabel(name: MusicBrainzName): string {
  if (name.target_type === "track") return `曲目的${(trackFieldLabels as Record<string, string>)[name.field] ?? name.field}`;
  return (correctionFieldLabels as Record<string, string>)[name.field] ?? name.field;
}

/** MusicBrainz 的写法：本地写法 → MusicBrainz 写法；能采用时行尾一键采用，依据过期时就近提示。 */
function MusicBrainzNames({ releaseId, names, refresh }: { releaseId: string; names: readonly MusicBrainzName[]; refresh: () => void }) {
  const adopt = useAdoptMusicBrainzName(releaseId, refresh);
  return (
    <>
      <p className={styles.namesTitle}>MusicBrainz 的写法</p>
      <ul className={styles.list}>
        {names.map((name) => (
          <li key={`${name.target_id}/${name.field}`} className={styles.row}>
            <span className={styles.name}>
              <span className={styles.field}>{nameFieldLabel(name)}</span>
              {name.effective_value} → {name.musicbrainz_value}
            </span>
            {name.can_adopt ? (
              <Button
                variant="icon"
                icon={<ArrowLeftRight size={16} />}
                label={`采用 MusicBrainz 的写法：${name.musicbrainz_value}`}
                disabled={adopt.isPending}
                onClick={() => adopt.mutate({ targetId: name.target_id, field: name.field, basisOperationId: name.basis_operation_id, revision: name.revision })}
              />
            ) : null}
          </li>
        ))}
      </ul>
      {adopt.isError ? <FailureNotice error={adopt.error} context="write" messages={{ revision_conflict: "写法刚变过，页面刷新后请再看一下。", item_stale: "依据已经变了，页面刷新后请再看一下。" }} /> : null}
    </>
  );
}

function MatchResultRow({ releaseId, result, refresh }: { releaseId: string; result: ReleaseMatchResultDTO; refresh: () => void }) {
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
      {result.status === "conflict" ? (
        <div className={styles.candidates}>
          <MatchCandidates releaseId={releaseId} source={result.source} snapshotId={result.snapshot_id} candidates={result.candidates} refresh={refresh} />
        </div>
      ) : null}
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
