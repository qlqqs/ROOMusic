import { Link } from "@tanstack/react-router";
import { BadgeCheck, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { DataTable, StatusTag, VisuallyHidden, type Column } from "../../../shared/ui";
import { albumArtistOf, evidenceFieldLabel, reasonCodeLabel } from "../../catalog";
import type { FieldIssueDTO, GroupingIssueDTO, IssueReviewDTO, MissingTrackDTO, MissingTracksIssueDTO, ReleaseIssuesDTO } from "../api/decoders";
import styles from "./release-issues-detail.module.css";

type ReleaseIssuesDetailProps = {
  issues: ReleaseIssuesDTO;
  /** 每节末尾的操作（核对、取消核对、编辑发行信息）：由调用方提供，这个组件只负责呈现问题。 */
  renderFieldActions?: ((fields: readonly FieldIssueDTO[]) => ReactNode) | undefined;
  renderReviewAction?: ((issue: ReviewSubject) => ReactNode) | undefined;
};

/** 可以核对的一类问题：字段（来源类型、介质类型）或者整节（归组冲突、缺失曲目）。 */
export type ReviewSubject =
  | { issue: "source_type" | "media_type"; basis: string; status: "open" | "reviewed"; review: IssueReviewDTO | null }
  | { issue: "grouping" | "missing_tracks"; basis: string; status: "open" | "reviewed"; review: IssueReviewDTO | null };

const none = (
  <>
    <span aria-hidden="true">—</span>
    <VisuallyHidden>没有</VisuallyHidden>
  </>
);

/** 核对状态：“已核对 · 用户名 · 时间”；有核对行但问题又是待处理，说明“核对之后情况变了”。 */
function ReviewState({ status, review, staleText }: { status: "open" | "reviewed"; review: IssueReviewDTO | null; staleText: string }) {
  if (review === null) return null;
  return (
    <p className={styles.review}>
      <StatusTag tone="accent" icon={<BadgeCheck size={12} />}>
        已核对
      </StatusTag>{" "}
      <span>
        {review.reviewer.username} · {formatDateTime(review.reviewed_at)}
      </span>
      {status === "open" ? (
        <span className={styles.stale}>
          <TriangleAlert size={14} aria-hidden="true" /> {staleText}
        </span>
      ) : null}
    </p>
  );
}

function FieldsSection({ fields, renderFieldActions, renderReviewAction }: { fields: readonly FieldIssueDTO[] } & Omit<ReleaseIssuesDetailProps, "issues">) {
  const columns: readonly Column<FieldIssueDTO>[] = [
    { id: "field", header: "字段", rowHeader: true, cell: (field) => <span className={styles.fieldName}>{evidenceFieldLabel(field.field)}</span> },
    { id: "value", header: "当前值", cell: (field) => field.value ?? none },
    { id: "reason", header: "原因", cell: (field) => (field.reason_code === null ? none : reasonCodeLabel(field.reason_code)) },
    {
      id: "candidates",
      header: "各曲目的值",
      cell: (field) => (field.candidates.length === 0 ? none : field.candidates.join("、")),
    },
    {
      id: "state",
      header: "处理",
      cell: (field) =>
        field.reviewable && field.basis !== null ? (
          <div className={styles.fieldState}>
            <ReviewState status={field.status} review={field.review} staleText="核对之后各曲目的值变了" />
            {renderReviewAction?.({ issue: field.field as "source_type" | "media_type", basis: field.basis, status: field.status, review: field.review })}
          </div>
        ) : (
          "在“编辑发行信息”里修改或确认扫描值"
        ),
    },
  ];
  return (
    <section className={styles.section} aria-labelledby="issues-fields">
      <h3 id="issues-fields">拿不准的字段</h3>
      <p className={styles.explain}>扫描没有把握的字段。来源类型与介质类型不能修改，看过之后可以标记为已核对；其余字段在“编辑发行信息”里修改或确认扫描值。</p>
      <DataTable caption="拿不准的字段" columns={columns} rows={fields} rowKey={(field) => field.field} />
      {renderFieldActions?.(fields)}
    </section>
  );
}

function GroupingSection({ grouping, renderReviewAction }: { grouping: GroupingIssueDTO } & Pick<ReleaseIssuesDetailProps, "renderReviewAction">) {
  return (
    <section className={styles.section} aria-labelledby="issues-grouping">
      <h3 id="issues-grouping">同一目录的归组冲突</h3>
      <p className={styles.explain}>
        {`${grouping.reason_codes.map((code) => reasonCodeLabel(code)).join("；")}：这个目录里各曲目的专辑标签没有过半数，扫描按标签拆成了几个版本。如果它们其实是同一张专辑，可以在专辑页“归入其他专辑”；确认拆分没有问题，就标记为已核对。`}
      </p>
      <ReviewState status={grouping.status} review={grouping.review} staleText="核对之后涉及的文件变了" />
      {renderReviewAction?.({ issue: "grouping", basis: grouping.basis, status: grouping.status, review: grouping.review })}
      <div className={styles.facts}>
        <h4>涉及的文件</h4>
        <ul className={styles.paths}>
          {grouping.source_refs.map((ref) => (
            <li key={ref}>{ref}</li>
          ))}
        </ul>
        {grouping.siblings.length === 0 ? null : (
          <>
            <h4>同一目录的其他版本</h4>
            <ul className={styles.siblings}>
              {grouping.siblings.map((sibling) => (
                <li key={sibling.release_id}>
                  <Link to="/albums/$groupId" params={{ groupId: sibling.group_id }} search={{ release: sibling.release_id }}>
                    {sibling.title}
                  </Link>
                </li>
              ))}
            </ul>
          </>
        )}
        {grouping.truncated ? <p className={styles.note}>内容较多，这里只列出一部分。</p> : null}
      </div>
    </section>
  );
}

function MissingTracksSection({ missing, renderReviewAction }: { missing: MissingTracksIssueDTO } & Pick<ReleaseIssuesDetailProps, "renderReviewAction">) {
  const columns: readonly Column<MissingTrackDTO>[] = [
    { id: "title", header: "曲目", rowHeader: true, cell: (track) => track.title },
    { id: "position", header: "碟与曲序", cell: (track) => `第 ${track.medium_position} 张 · 第 ${track.position} 首` },
    { id: "source", header: "文件", cell: (track) => track.source },
    { id: "seen", header: "最后一次看到", cell: (track) => formatDateTime(track.last_seen_at) },
    {
      id: "state",
      header: "状态",
      cell: (track) => (track.reviewed ? "已核对" : <strong className={styles.openTrack}>待处理</strong>),
    },
  ];
  return (
    <section className={styles.section} aria-labelledby="issues-missing">
      <h3 id="issues-missing">缺失的曲目（{formatCount(missing.count)} 首）</h3>
      <p className={styles.explain}>完整扫描时找不到这些文件。把文件放回原处并重新扫描，它们会自动恢复；确认文件已不需要，就标记为已核对，之后新缺失的曲目仍会提醒。</p>
      <ReviewState status={missing.status} review={missing.review} staleText="核对之后又有曲目缺失" />
      {renderReviewAction?.({ issue: "missing_tracks", basis: missing.basis, status: missing.status, review: missing.review })}
      <DataTable caption="缺失的曲目" columns={columns} rows={missing.tracks} rowKey={(track) => track.id} />
      {missing.truncated ? <p className={styles.note}>只列出前 {formatCount(missing.tracks.length)} 首；核对覆盖全部 {formatCount(missing.count)} 首。</p> : null}
    </section>
  );
}

/**
 * 一个版本的问题：头部是专辑名与“需要关注”，然后逐节列出拿不准的字段、同一目录的归组冲突与缺失的曲目（没有的节不出现）。
 * 都没有时说明“这个版本已经没有待处理的问题”（处理完之后版本仍保持选中，页面不跳走）。
 */
export function ReleaseIssuesDetail({ issues, renderFieldActions, renderReviewAction }: ReleaseIssuesDetailProps) {
  const { release } = issues;
  const empty = issues.fields.length === 0 && issues.grouping === null && issues.missing_tracks === null;
  return (
    <article className={styles.detail} aria-labelledby="issues-detail-title">
      <header className={styles.header}>
        <h2 id="issues-detail-title" className={styles.title}>
          {release.title}
        </h2>
        <p className={styles.meta}>
          {[albumArtistOf(release), issues.edition].filter((part) => part !== null).join(" · ")}
        </p>
        <p className={styles.meta}>
          {release.attention_count === 0 ? "这个版本已经没有待处理的问题。" : `需要关注：${formatCount(release.attention_count)}`}{" "}
          <Link to="/albums/$groupId" params={{ groupId: issues.group_id }} search={{ release: release.id }}>
            打开这张专辑
          </Link>
        </p>
      </header>
      {empty ? <p>扫描结论里已经没有问题了。</p> : null}
      {issues.fields.length === 0 ? null : <FieldsSection fields={issues.fields} renderFieldActions={renderFieldActions} renderReviewAction={renderReviewAction} />}
      {issues.grouping === null ? null : <GroupingSection grouping={issues.grouping} renderReviewAction={renderReviewAction} />}
      {issues.missing_tracks === null ? null : <MissingTracksSection missing={issues.missing_tracks} renderReviewAction={renderReviewAction} />}
    </article>
  );
}
