import { Link, useNavigate } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { formatDateTime, UNKNOWN_TIME } from "../../../shared/lib/time";
import { DataTable, Pagination, Panel, QueryContent, Skeleton, VisuallyHidden, type Column } from "../../../shared/ui";
import type { ScanDTO } from "../api/decoders";
import { useScanHistory } from "../hooks/use-scan-history";
import { scanPhase } from "../scan-status";
import { ScanPhaseTag } from "./scan-phase-tag";
import styles from "./scan-history-section.module.css";

const columns: Column<ScanDTO>[] = [
  { id: "started", header: "开始时间", rowHeader: true, cell: (scan) => formatDateTime(scan.started_at) },
  { id: "status", header: "状态", cell: (scan) => <ScanPhaseTag phase={scanPhase(scan)} /> },
  {
    id: "finished",
    header: "结束时间",
    cell: (scan) => (scan.status === "running" ? "进行中" : scan.finished_at === null ? UNKNOWN_TIME : formatDateTime(scan.finished_at)),
  },
  { id: "diagnostics", header: "诊断数", numeric: true, cell: (scan) => formatCount(scan.diagnostics.total) },
  {
    id: "detail",
    header: "详情",
    hideLabelWhenStacked: true,
    // 每一行的链接文字相同：开始时间放进可访问名称，读屏的链接列表里能分辨是哪一次扫描。
    cell: (scan) => (
      <Link to="/settings/library/scans/$scanId" params={{ scanId: scan.id }}>
        查看详情
        <VisuallyHidden>（{formatDateTime(scan.started_at)} 开始的扫描）</VisuallyHidden>
      </Link>
    ),
  },
];

/**
 * 扫描目录页的“扫描记录”区段：已结束与进行中的扫描，开始时间最新的在前，每行可进入扫描详情。
 * 页码来自 URL（scan_page）：页码超出末页时用替换历史的方式改到末页（没有数据时回到第 1 页）。
 * 诊断数是扫描发现的问题数，不是进度。
 */
export function ScanHistorySection({ page }: { page: number }) {
  const history = useScanHistory(page);
  const navigate = useNavigate();

  const pagination = history.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !history.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/settings/library", search: (previous) => ({ ...previous, scan_page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  return (
    <Panel title="扫描记录">
      <QueryContent query={history} loading={<Skeleton label="正在加载扫描记录" lines={3} />}>
        {(data) =>
          outOfRange ? (
            <Skeleton label="正在加载扫描记录" lines={3} />
          ) : data.pagination.total === 0 ? (
            <p>还没有扫描记录。</p>
          ) : (
            <div aria-busy={history.isPlaceholderData}>
              <DataTable caption="扫描记录" columns={columns} rows={data.items} rowKey={(scan) => scan.id} />
              <p className={styles.note}>
                共 {formatCount(data.pagination.total)} 次扫描
              </p>
              <Pagination
                label="扫描记录分页"
                page={data.pagination.page}
                pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                renderLink={(target, props) => (
                  <Link to="/settings/library" search={(previous) => ({ ...previous, scan_page: target > 1 ? target : undefined })} {...props} />
                )}
              />
            </div>
          )
        }
      </QueryContent>
    </Panel>
  );
}
