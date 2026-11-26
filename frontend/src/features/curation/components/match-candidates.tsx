import { Check } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { Button, FailureNotice } from "../../../shared/ui";
import type { MatchCandidateDTO } from "../api/decoders";
import { useSelectMatchCandidate } from "../hooks/use-match-decisions";
import styles from "./match-candidates.module.css";

/** 候选的一行说明：标题、艺人，再加日期、地区、曲数（有才写）。 */
function candidateFacts(candidate: MatchCandidateDTO): string {
  return [candidate.date, candidate.country, candidate.track_count > 0 ? `${formatCount(candidate.track_count)} 首` : ""].filter((part) => part !== "").join(" · ");
}

/**
 * 有冲突时几张都对得上的候选（ADR 0009 第 6.3 节）：每张一行，行尾“选这一版”。选中的会重新做结构核对再写入；
 * 不选就保持原样。整理页的“有冲突的专辑”和专辑详情的“外部识别”共用。
 */
export function MatchCandidates({
  releaseId,
  source,
  snapshotId,
  candidates,
  refresh,
}: {
  releaseId: string;
  source: string;
  snapshotId: string | null;
  candidates: readonly MatchCandidateDTO[];
  refresh: () => void;
}) {
  const select = useSelectMatchCandidate(releaseId, refresh);
  if (snapshotId === null || candidates.length === 0) return null;
  const pending = select.isPending ? select.variables.externalId : null;
  return (
    <>
      <ul className={styles.list}>
        {candidates.map((candidate) => {
          const facts = candidateFacts(candidate);
          const name = candidate.title === "" ? candidate.external_id.slice(0, 8) : candidate.title;
          return (
            <li key={candidate.external_id} className={styles.row}>
              <span className={styles.body}>
                <span className={styles.title}>
                  {name}
                  {candidate.artist === "" ? null : <span className={styles.artist}> · {candidate.artist}</span>}
                </span>
                {facts === "" ? null : <span className={styles.facts}>{facts}</span>}
              </span>
              <Button
                variant="icon"
                icon={<Check size={16} />}
                label={`选这一版：${name}`}
                tooltip="选这一版（会重新核对曲目结构再写入）"
                disabled={select.isPending || select.isSuccess}
                onClick={() => select.mutate({ source, snapshotId, externalId: candidate.external_id })}
              />
              {pending === candidate.external_id ? <span className={styles.pending}>正在核对…</span> : null}
            </li>
          );
        })}
      </ul>
      {select.isError ? (
        <FailureNotice
          error={select.error}
          context="write"
          messages={{ item_stale: "候选已经变了，页面刷新后请再看一下。", insufficient_corroboration: "这一版的曲目结构和本地对不上，没有写入。" }}
        />
      ) : null}
    </>
  );
}
