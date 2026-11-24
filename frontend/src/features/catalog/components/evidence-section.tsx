import { useId, useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Button, DataTable, Disclosure, FailureNotice, Notice, Skeleton, type Column } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseEvidenceDTO, ReleaseEvidenceSummaryDTO, ReleaseFieldEvidenceDTO, ReleaseGroupingEvidenceDTO } from "../api/decoders";
import { fieldMarksOf, type FieldMarks } from "../format";
import { useReleaseEvidence } from "../hooks/use-release-evidence";
import { candidateKindLabel, confidenceLabel, evidenceActionLabel, evidenceFieldLabel, evidenceSourceLabel, reasonCodeLabel } from "../labels";
import styles from "./evidence-section.module.css";
import { FieldMarkTag, hasFieldMark } from "./field-mark-tag";

/** 字段名加上来源标记：证据照旧写扫描得到的结论，标记说明页面显示的是管理员改过的值或自动整理补上的值。 */
function fieldCell(field: string, marks: FieldMarks) {
  return hasFieldMark(marks, field) ? (
    <span className={styles.fieldCell}>
      {evidenceFieldLabel(field)}
      <FieldMarkTag marks={marks} field={field} />
    </span>
  ) : (
    evidenceFieldLabel(field)
  );
}

// 摘要所有登录成员都能看：只写字段、来源、可信度与处理方式。整理规则的内部代号（majority_v1 之类）对读者没有意义，
// 只在管理员的完整证据（归组依据）里出现。
const summaryColumns = (marks: FieldMarks): Column<ReleaseEvidenceSummaryDTO>[] => [
  { id: "field", header: "字段", rowHeader: true, cell: (item) => fieldCell(item.field, marks) },
  { id: "source", header: "来源", cell: (item) => evidenceSourceLabel(item.source) },
  { id: "confidence", header: "可信度", cell: (item) => confidenceLabel(item.confidence) },
  { id: "action", header: "处理方式", cell: (item) => evidenceActionLabel(item.action) },
];

const none = "—";

const fieldColumns = (marks: FieldMarks): Column<ReleaseFieldEvidenceDTO>[] => [
  { id: "field", header: "字段", rowHeader: true, cell: (item) => fieldCell(item.field, marks) },
  { id: "value", header: "取值", cell: (item) => (item.value === null || item.value === "" ? none : item.value) },
  {
    id: "candidates",
    header: "候选值",
    cell: (item) =>
      item.candidates.length === 0 ? (
        none
      ) : (
        <ul className={styles.plainList}>
          {item.candidates.map((candidate, index) => (
            <li key={index}>{candidate}</li>
          ))}
        </ul>
      ),
  },
  { id: "reason", header: "原因", cell: (item) => (item.reason_code === null ? none : reasonCodeLabel(item.reason_code)) },
];

function GroupingEvidence({ grouping }: { grouping: ReleaseGroupingEvidenceDTO | null }) {
  if (grouping === null) return <p>没有归组证据。</p>;
  return (
    <dl className={styles.facts}>
      <dt>归组方式</dt>
      <dd>{candidateKindLabel(grouping.candidate_kind)}</dd>
      <dt>规则</dt>
      <dd>
        <code>{grouping.rule_id}</code>
      </dd>
      <dt>原因</dt>
      <dd>
        {grouping.reason_codes.length === 0 ? (
          none
        ) : (
          <ul className={styles.plainList}>
            {grouping.reason_codes.map((code, index) => (
              <li key={index}>{reasonCodeLabel(code)}</li>
            ))}
          </ul>
        )}
      </dd>
      <dt>来源引用</dt>
      <dd>
        {grouping.source_refs.length === 0 ? (
          none
        ) : (
          <ul className={styles.plainList}>
            {grouping.source_refs.map((ref, index) => (
              <li key={index}>
                <code className={styles.path}>{ref}</code>
              </li>
            ))}
          </ul>
        )}
      </dd>
    </dl>
  );
}

function EvidenceDetail({ evidence, marks }: { evidence: ReleaseEvidenceDTO; marks: FieldMarks }) {
  return (
    <div className={styles.detail}>
      {evidence.truncated ? <Notice tone="warning">证据太长，只显示了前面一部分。</Notice> : null}
      {evidence.fields.length === 0 ? (
        <p>没有字段证据。</p>
      ) : (
        <DataTable caption="字段证据" columns={fieldColumns(marks)} rows={evidence.fields} rowKey={(item) => item.field} />
      )}
      <h4 className={styles.subheading}>归组依据</h4>
      <GroupingEvidence grouping={evidence.grouping} />
    </div>
  );
}

// 完整证据接口只有管理员能读：403 只会在角色刚被改动时出现，404 表示版本已经不可见。
const failureMessages = {
  forbidden: "你没有权限查看完整证据。",
  not_found: "这个版本已经找不到了，可能刚重新扫描过。",
} as const;

/**
 * 管理员的完整证据（字段值、候选值、原因码、归组依据）。点了按钮才请求，按钮留在原处：加载中忙碌、之后可重新加载，
 * 焦点不丢；失败就地提示，不影响页面其他部分。
 */
function FullEvidence({ releaseId, marks }: { releaseId: string; marks: FieldMarks }) {
  const headingId = useId();
  const [requested, setRequested] = useState(false);
  const evidence = useReleaseEvidence(releaseId, requested);
  const loading = requested && evidence.isFetching;
  return (
    <section className={styles.full} aria-labelledby={headingId}>
      <h3 id={headingId} className={styles.subheading}>
        完整证据
      </h3>
      <div>
        <Button busy={loading} onClick={() => (requested ? void evidence.refetch() : setRequested(true))}>
          {loading ? "正在加载…" : requested ? "重新加载完整证据" : "查看完整证据"}
        </Button>
      </div>
      {!requested ? null : evidence.data !== undefined ? (
        <>
          {evidence.isError ? <FailureNotice error={evidence.error} messages={failureMessages} /> : null}
          <EvidenceDetail evidence={evidence.data} marks={marks} />
        </>
      ) : evidence.isError ? (
        <FailureNotice error={evidence.error} messages={failureMessages} />
      ) : (
        <Skeleton label="正在加载完整证据" lines={4} />
      )}
    </section>
  );
}

/**
 * 详情底部折叠的“来源与证据”。所有登录用户都能读摘要（来自版本详情，字段、来源、可信度与处理方式）；
 * 管理员在展开后多一个“查看完整证据”，其余用户从不请求管理员接口。换版本时（key）回到折叠。
 */
/** “来源与证据”折叠区块的 id：页头“需要关注”旁的入口据此展开它。 */
export const EVIDENCE_SECTION_ID = "release-evidence";

export function EvidenceSection({ release, isAdmin }: { release: ReleaseDetailDTO; isAdmin: boolean }) {
  const count = release.evidence.length;
  return (
    <Disclosure id={EVIDENCE_SECTION_ID} title="来源与证据" detail={`（${formatCount(count)} 项）`}>
      {release.corrected_fields.length === 0 ? null : (
        <p className={styles.note}>标有“人工修正”的字段，页面上显示的是改过的值。</p>
      )}
      {(release.auto_fields ?? []).length === 0 ? null : (
        <p className={styles.note}>标有“自动补全”的字段，页面上显示的是自动整理从外部资料补上的值。</p>
      )}
      {count === 0 ? (
        <p>这个版本没有记录证据。</p>
      ) : (
        <DataTable caption="证据摘要" columns={summaryColumns(fieldMarksOf(release))} rows={release.evidence} rowKey={(item) => item.field} />
      )}
      {isAdmin ? <FullEvidence releaseId={release.id} marks={fieldMarksOf(release)} /> : null}
    </Disclosure>
  );
}
