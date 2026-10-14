import { useId, useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Button, DataTable, Disclosure, FailureNotice, Notice, Skeleton, type Column } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseEvidenceDTO, ReleaseEvidenceSummaryDTO, ReleaseFieldEvidenceDTO, ReleaseGroupingEvidenceDTO } from "../api/decoders";
import { isFieldCorrected } from "../format";
import { useReleaseEvidence } from "../hooks/use-release-evidence";
import { candidateKindLabel, confidenceLabel, evidenceActionLabel, evidenceFieldLabel, evidenceSourceLabel, reasonCodeLabel } from "../labels";
import styles from "./evidence-section.module.css";
import { ManualCorrectionTag } from "./manual-correction-tag";

/** 字段名加上人工修正的标记：证据照旧写扫描得到的结论，标记说明页面显示的是管理员改过的值。 */
function fieldCell(field: string, corrected: readonly string[]) {
  return isFieldCorrected(corrected, field) ? (
    <span className={styles.fieldCell}>
      {evidenceFieldLabel(field)}
      <ManualCorrectionTag />
    </span>
  ) : (
    evidenceFieldLabel(field)
  );
}

const summaryColumns = (corrected: readonly string[]): Column<ReleaseEvidenceSummaryDTO>[] => [
  { id: "field", header: "字段", rowHeader: true, cell: (item) => fieldCell(item.field, corrected) },
  { id: "source", header: "来源", cell: (item) => evidenceSourceLabel(item.source) },
  { id: "confidence", header: "可信度", cell: (item) => confidenceLabel(item.confidence) },
  { id: "action", header: "处理方式", cell: (item) => evidenceActionLabel(item.action) },
  { id: "rule", header: "规则", cell: (item) => <code>{item.rule_id}</code> },
];

const none = "—";

const fieldColumns = (corrected: readonly string[]): Column<ReleaseFieldEvidenceDTO>[] => [
  { id: "field", header: "字段", rowHeader: true, cell: (item) => fieldCell(item.field, corrected) },
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

function EvidenceDetail({ evidence, corrected }: { evidence: ReleaseEvidenceDTO; corrected: readonly string[] }) {
  return (
    <div className={styles.detail}>
      {evidence.truncated ? <Notice tone="warning">证据内容过长，已被截断，下面只显示前面的部分。</Notice> : null}
      {evidence.fields.length === 0 ? (
        <p>没有字段证据。</p>
      ) : (
        <DataTable caption="字段证据" columns={fieldColumns(corrected)} rows={evidence.fields} rowKey={(item) => item.field} />
      )}
      <h4 className={styles.subheading}>归组依据</h4>
      <GroupingEvidence grouping={evidence.grouping} />
    </div>
  );
}

// 完整证据接口只有管理员能读：403 只会在角色刚被改动时出现，404 表示版本已经不可见。
const failureMessages = {
  forbidden: "你的账号没有查看完整证据的权限。",
  not_found: "这个版本已不可见（可能刚被重新扫描），无法读取完整证据。",
} as const;

/**
 * 管理员的完整证据（字段值、候选值、原因码、归组依据）。点了按钮才请求，按钮留在原处：加载中忙碌、之后可重新加载，
 * 焦点不丢；失败就地提示，不影响页面其他部分。
 */
function FullEvidence({ releaseId, corrected }: { releaseId: string; corrected: readonly string[] }) {
  const headingId = useId();
  const [requested, setRequested] = useState(false);
  const evidence = useReleaseEvidence(releaseId, requested);
  const loading = requested && evidence.isFetching;
  return (
    <section className={styles.full} aria-labelledby={headingId}>
      <h3 id={headingId} className={styles.subheading}>
        完整证据
      </h3>
      <p className={styles.note}>字段取值、候选值、原因码与归组依据，只有管理员能看到。</p>
      <div>
        <Button busy={loading} onClick={() => (requested ? void evidence.refetch() : setRequested(true))}>
          {loading ? "正在加载…" : requested ? "重新加载完整证据" : "查看完整证据"}
        </Button>
      </div>
      {!requested ? null : evidence.data !== undefined ? (
        <>
          {evidence.isError ? <FailureNotice error={evidence.error} messages={failureMessages} /> : null}
          <EvidenceDetail evidence={evidence.data} corrected={corrected} />
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
export function EvidenceSection({ release, isAdmin }: { release: ReleaseDetailDTO; isAdmin: boolean }) {
  const count = release.evidence.length;
  return (
    <Disclosure title="来源与证据" detail={`（${formatCount(count)} 项）`}>
      <p className={styles.note}>
        这里记录各项信息是怎样得到的。“需要关注”只是提示依据不够确定，或有来源文件暂时找不到，需要人工核对，并不表示信息有误。
      </p>
      {release.corrected_fields.length === 0 ? null : (
        <p className={styles.note}>标有“人工修正”的字段，页面显示的是管理员改过的值；下面仍然记录扫描得到的结论，重新扫描不会覆盖人工修正。</p>
      )}
      {count === 0 ? (
        <p>这个版本没有记录证据。</p>
      ) : (
        <DataTable caption="证据摘要" columns={summaryColumns(release.corrected_fields)} rows={release.evidence} rowKey={(item) => item.field} />
      )}
      {isAdmin ? <FullEvidence releaseId={release.id} corrected={release.corrected_fields} /> : null}
    </Disclosure>
  );
}
