import { Link } from "@tanstack/react-router";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { Pagination, QueryContent, SettingsGroup, Skeleton } from "../../../shared/ui";
import { sourceName } from "../../governance";
import { conflictReasonText } from "../curation-text";
import { useCurationConflicts } from "../hooks/use-match-decisions";
import { MatchCandidates } from "./match-candidates";
import styles from "./curation-sections.module.css";

/**
 * 整理页的“有冲突的专辑”（ADR 0009 第 6.3 节）：几版都对得上、分不出是哪一版的，列出候选，点一下就能定；不点就保持原样。
 * 选中的照样过结构核对。page 来自地址（第 1 页没有 conflict_page 参数）；refresh 让目录与详情跟着更新（由 app 提供）。
 */
export function CurationConflictsSection({ page, refresh }: { page: number; refresh: () => void }) {
  const conflicts = useCurationConflicts(page);
  return (
    <SettingsGroup
      title="有冲突的专辑"
      footer={
        conflicts.data === undefined ? null : (
          <Pagination
            label="有冲突的专辑分页"
            page={conflicts.data.pagination.page}
            pageCount={pageCountOf(conflicts.data.pagination.total, conflicts.data.pagination.page_size)}
            renderLink={(targetPage, props) => (
              <Link to="/settings/curation" search={(previous) => ({ ...previous, conflict_page: targetPage > 1 ? targetPage : undefined })} {...props} />
            )}
          />
        )
      }
    >
      <QueryContent query={conflicts} loading={<Skeleton label="正在加载有冲突的专辑" lines={3} />}>
        {(data) =>
          data.items.length === 0 ? (
            <p className={styles.groupNote}>没有分不出版本的专辑。</p>
          ) : (
            <ul className={styles.linkList}>
              {data.items.map((item) => (
                <li key={`${item.release_id}/${item.source}`}>
                  <p>
                    <strong>{item.title === "" ? "（无标题）" : item.title}</strong>
                    <span className={styles.meta}>
                      {" "}
                      · {sourceName(item.source)} · {conflictReasonText(item.reason, item.conflicts)}
                      {item.checked_at === null ? "" : ` · ${formatDateTime(item.checked_at)} 查询`}
                    </span>
                  </p>
                  <MatchCandidates releaseId={item.release_id} source={item.source} snapshotId={item.snapshot_id} candidates={item.candidates} refresh={refresh} />
                </li>
              ))}
            </ul>
          )
        }
      </QueryContent>
    </SettingsGroup>
  );
}
