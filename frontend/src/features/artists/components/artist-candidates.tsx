import { Check, Globe, LoaderCircle } from "lucide-react";
import { useTooltip } from "../../../shared/ui";
import type { ArtistCandidateDTO } from "../api/decoders";
import styles from "./artist-candidates.module.css";
import { CitationRows } from "./citation-rows";

function AcceptButton({ candidate, disabled, onAccept }: { candidate: ArtistCandidateDTO; disabled: boolean; onAccept: () => void }) {
  const label = `就是 ${candidate.name}`;
  const tip = useTooltip(label);
  return (
    <>
      <button type="button" className={styles.accept} aria-label={label} aria-disabled={disabled ? true : undefined} onClick={() => !disabled && onAccept()} {...tip.triggerProps}>
        <Check size={15} aria-hidden="true" />
      </button>
      {tip.tooltip}
    </>
  );
}

/**
 * 网络搜索给出的身份候选（审查转给人工的）：每个候选一行——名字的首字、名字、简短说明、条目号与引用网页数，右侧一个勾采纳；
 * 展开引用可以看模型依据的网页。采纳中显示转圈，期间不能再点。
 */
export function ArtistCandidates({ candidates, deciding, onAccept }: { candidates: readonly ArtistCandidateDTO[]; deciding: boolean; onAccept: (candidate: ArtistCandidateDTO) => void }) {
  return (
    <ul className={styles.list} aria-label="身份候选">
      {candidates.map((candidate) => (
        <li key={candidate.exception_id} className={styles.candidate}>
          <span className={styles.disc} aria-hidden="true">
            {Array.from(candidate.name)[0] ?? ""}
          </span>
          <span className={styles.body}>
            <span className={styles.name}>{candidate.name}</span>
            {candidate.description === null ? null : <span className={styles.description}>{candidate.description}</span>}
            <span className={styles.meta}>
              <span>{candidate.source === "wikidata" ? candidate.external_id : candidate.external_id.slice(0, 8)}</span>
              {candidate.citations.length === 0 ? null : (
                <details className={styles.citations}>
                  <summary aria-label={`${candidate.citations.length} 个依据网页`}>
                    <Globe size={12} aria-hidden="true" />
                    {candidate.citations.length}
                  </summary>
                  <div className={styles.citationList}>
                    <CitationRows citations={candidate.citations} />
                  </div>
                </details>
              )}
            </span>
          </span>
          {deciding ? <LoaderCircle className={styles.spin} size={15} aria-label="正在处理" /> : <AcceptButton candidate={candidate} disabled={deciding} onAccept={() => onAccept(candidate)} />}
        </li>
      ))}
    </ul>
  );
}
