import { RefreshCw } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { Button, DataTable, Panel, QueryContent, Skeleton, type Column } from "../../../shared/ui";
import type { DiagnosticDTO, DiagnosticsPageDTO } from "../api/decoders";
import { diagnosticKindLabel } from "../diagnostic-kinds";
import { useScanDiagnostics } from "../hooks/use-scan-diagnostics";
import styles from "./scan-diagnostics.module.css";

/** 后端最多返回前 100 条明细；总数超过时提示这不是全部。 */
const DETAIL_LIMIT = 100;

const columns: Column<DiagnosticDTO>[] = [
  { id: "kind", header: "类别", cell: (item) => diagnosticKindLabel(item.kind) },
  { id: "message", header: "说明", cell: (item) => item.message },
  // path 只在后端确认是安全的相对路径时才有。
  { id: "path", header: "路径", cell: (item) => (item.path === null ? "—" : <code>{item.path}</code>) },
];

function DiagnosticsBody({ page, finished }: { page: DiagnosticsPageDTO; finished: boolean }) {
  if (page.summary.total === 0) {
    return <p>{finished ? "本次扫描没有发现需要检查的问题。" : "到目前为止没有发现需要检查的问题。"}</p>;
  }
  return (
    <>
      <ul className={styles.kinds} aria-label="按类别汇总">
        {page.summary.counts.map((entry) => (
          <li key={entry.kind} className={styles.kind}>
            <span>{diagnosticKindLabel(entry.kind)}</span>
            <span className={styles.count}>{formatCount(entry.count)}</span>
          </li>
        ))}
      </ul>
      <DataTable caption="诊断明细" columns={columns} rows={page.items} rowKey={(item) => item.id} />
      {page.summary.total > page.items.length ? (
        <p className={styles.note}>
          共 {formatCount(page.summary.total)} 条诊断，只显示前 {formatCount(Math.min(DETAIL_LIMIT, page.items.length))} 条明细。
        </p>
      ) : null}
    </>
  );
}

/**
 * 扫描的诊断：按类别汇总加最多 100 条明细（类别、安全说明、可选的相对路径）。不轮询：
 * 运行中可以手动刷新，扫描结束时自动刷新一次。
 */
export function ScanDiagnostics({ scanId, finished }: { scanId: string; finished: boolean }) {
  const diagnostics = useScanDiagnostics(scanId, finished);
  return (
    <Panel
      title="诊断"
      action={
        <Button variant="text" icon={<RefreshCw size={14} />} busy={diagnostics.isFetching} onClick={() => void diagnostics.refetch()}>
          {diagnostics.isFetching ? "正在刷新…" : "刷新诊断"}
        </Button>
      }
    >
      <QueryContent query={diagnostics} loading={<Skeleton label="正在加载诊断" lines={3} />}>
        {(page) => <DiagnosticsBody page={page} finished={finished} />}
      </QueryContent>
    </Panel>
  );
}
