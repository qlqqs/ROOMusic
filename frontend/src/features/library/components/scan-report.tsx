import { Link } from "@tanstack/react-router";
import { ArrowRightLeft } from "lucide-react";
import { useId, useRef, useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { Button, DataTable, Pagination, Panel, QueryContent, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import type { AddedAlbumDTO, ChangeCountsDTO, ScanDTO, ScanReportDTO } from "../api/decoders";
import { isReportUnavailable, useScanReport } from "../hooks/use-scan-report";
import styles from "./scan-report.module.css";

const UNAVAILABLE_TEXT = "这次的报告没能生成，扫描结果已经保存。";

type ChangeRow = { id: string; label: string; counts: ChangeCountsDTO };

const changeColumns: Column<ChangeRow>[] = [
  { id: "kind", header: "类别", cell: (row) => row.label, rowHeader: true },
  { id: "added", header: "新增", cell: (row) => formatCount(row.counts.added), numeric: true },
  { id: "updated", header: "更新", cell: (row) => formatCount(row.counts.updated), numeric: true },
  { id: "missing", header: "消失", cell: (row) => formatCount(row.counts.missing), numeric: true },
];

function filesText(report: ScanReportDTO): string {
  const { files } = report;
  const processed = `${report.status === "ready" ? "发现" : "已处理"} ${formatCount(files.processed)} 个`;
  return files.changed && files.total !== null ? `${processed}（数文件时是 ${formatCount(files.total)} 个）` : processed;
}

function issuesText(report: ScanReportDTO): string {
  const { issues } = report;
  if (issues.may_be_truncated) return `至少 ${formatCount(issues.recorded)} 条`;
  return issues.recorded === 0 ? "没有" : `${formatCount(issues.recorded)} 条`;
}

/** 一张新增的专辑：标题与艺人是扫描时的样子；现在还看得到就链接到它现在所在的专辑，可能接替了旧版本时给出认领入口。 */
function AddedAlbum({ item }: { item: AddedAlbumDTO }) {
  const title = item.title_at_scan.trim() === "" ? "（没有标题）" : item.title_at_scan;
  return (
    <li className={styles.album}>
      <span className={styles.albumLine}>
        {item.album !== null && item.current_group_id !== null ? (
          <Link to="/albums/$groupId" params={{ groupId: item.current_group_id }} className={styles.albumTitle}>
            {title}
          </Link>
        ) : (
          <span className={styles.albumTitle}>{title}</span>
        )}
        <span className={styles.artist}>{item.artist_at_scan}</span>
        {item.album === null ? <StatusTag tone="outline">现在看不到了</StatusTag> : null}
      </span>
      {item.claim_hints.length === 0 ? null : (
        <ul className={styles.hints} aria-label={`《${title}》可能接替了已不可见的版本`}>
          {item.claim_hints.map((hint) => (
            <li key={hint.id} className={styles.hint}>
              <ArrowRightLeft size={16} aria-hidden="true" className={styles.hintIcon} />
              <span>可能接替了《{hint.title}》</span>
              <Link
                to="/claims"
                search={{ release: hint.id, target: item.release_id }}
                className={styles.hintLink}
                aria-label={`查看并认领《${hint.title}》，它可能被《${title}》接替了`}
              >
                查看并认领
              </Link>
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

function AddedAlbums({ report, isUpdating, onPage }: { report: ScanReportDTO; isUpdating: boolean; onPage: (page: number) => void }) {
  const headingId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const { items, pagination } = report.added_albums;
  // 翻页之后把焦点带到小标题：新的一页从列表开头读起，不停在页底的分页按钮上。
  const goTo = (page: number) => {
    onPage(page);
    headingRef.current?.focus();
  };
  return (
    <section aria-labelledby={headingId} className={styles.added}>
      <h3 id={headingId} ref={headingRef} tabIndex={-1} className={styles.subheading}>
        新增的专辑
      </h3>
      {pagination.total === 0 ? (
        <p>这次没有新增专辑。</p>
      ) : (
        <>
          <ul className={styles.albums} aria-busy={isUpdating}>
            {items.map((item) => (
              <AddedAlbum key={item.release_id} item={item} />
            ))}
          </ul>
          <Pagination
            label="新增专辑分页"
            page={pagination.page}
            pageCount={pageCountOf(pagination.total, pagination.page_size)}
            renderLink={(target, props) => (
              <Button variant="text" onClick={() => goTo(target)}>
                {props.children}
              </Button>
            )}
          />
        </>
      )}
    </section>
  );
}

function ReportBody({ report, isUpdating, onPage }: { report: ScanReportDTO; isUpdating: boolean; onPage: (page: number) => void }) {
  const rows: ChangeRow[] = [
    { id: "albums", label: "专辑", counts: report.albums },
    { id: "tracks", label: "曲目", counts: report.tracks },
  ];
  return (
    <div className={styles.report}>
      {report.status === "partial" ? <p>扫描没有完成，这里只有停下之前已经保存的变化。</p> : null}
      <dl className={styles.facts}>
        <dt>文件</dt>
        <dd>{filesText(report)}</dd>
        <dt>问题</dt>
        <dd>{issuesText(report)}</dd>
      </dl>
      <DataTable caption="专辑与曲目的变化" columns={changeColumns} rows={rows} rowKey={(row) => row.id} />
      <AddedAlbums report={report} isUpdating={isUpdating} onPage={onPage} />
    </div>
  );
}

function ScanReportContent({ scanId }: { scanId: string }) {
  const [page, setPage] = useState(1);
  const report = useScanReport(scanId, page, true);
  if (report.status === "error" && isReportUnavailable(report.error)) {
    return (
      <Panel title="扫描报告">
        <p>{UNAVAILABLE_TEXT}</p>
      </Panel>
    );
  }
  return (
    <Panel title="扫描报告">
      <QueryContent query={report} loading={<Skeleton label="正在加载扫描报告" lines={4} />}>
        {(data) => <ReportBody report={data} isUpdating={report.isPlaceholderData} onPage={setPage} />}
      </QueryContent>
    </Panel>
  );
}

/**
 * 扫描报告（管理员的扫描详情）：发现的文件、问题条数、专辑与曲目的新增/更新/消失，以及新增专辑的列表。
 * 只有本功能上线后、已经结束的扫描才有：运行中与旧扫描不显示这一块；报告没能生成时只说一句。
 * 没有成功结束的扫描显示停下之前已经保存的那部分变化。
 */
export function ScanReport({ scan }: { scan: ScanDTO }) {
  switch (scan.report_status) {
    case undefined:
    case "legacy":
    case "pending":
      return null;
    case "unavailable":
      return (
        <Panel title="扫描报告">
          <p>{UNAVAILABLE_TEXT}</p>
        </Panel>
      );
    case "ready":
    case "partial":
      return <ScanReportContent key={scan.id} scanId={scan.id} />;
  }
}
