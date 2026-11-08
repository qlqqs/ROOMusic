import { CircleCheck, CircleX } from "lucide-react";
import { useMemo } from "react";
import { formatDateTime, UNKNOWN_TIME } from "../../../shared/lib/time";
import { DataTable, Panel, QueryContent, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import type { RootOperationDTO } from "../api/decoders";
import { useRootOperations } from "../hooks/use-root-operations";
import { useRoots } from "../hooks/use-roots";
import styles from "./root-operations-section.module.css";

const operationLabels: Record<RootOperationDTO["operation"], string> = {
  create: "新增",
  disable: "停用",
  restore: "恢复",
  exclude: "不扫描文件夹",
  unexclude: "恢复扫描文件夹",
};

/** 后端最多返回最近 100 条；达到上限时提示这不是全部历史。 */
const OPERATION_LIMIT = 100;

/** 扫描目录页的“目录操作记录”区段：目录的新增、停用、恢复与不扫描的子文件夹的增删，最新的在前。这是目录管理记录，不是通用审批或回滚点。 */
export function RootOperationsSection() {
  const operations = useRootOperations();
  // 与“音乐目录”区段共用同一个缓存条目，不会多一次请求。
  const roots = useRoots();
  const names = useMemo(() => new Map((roots.data ?? []).map((root) => [root.id, root.name])), [roots.data]);

  const columns: Column<RootOperationDTO>[] = [
    { id: "time", header: "时间", cell: (item) => formatDateTime(item.created_at) },
    { id: "root", header: "目录", cell: (item) => (item.root_id === undefined ? UNKNOWN_TIME : (names.get(item.root_id) ?? UNKNOWN_TIME)) },
    { id: "operation", header: "操作", cell: (item) => operationLabels[item.operation] },
    {
      id: "status",
      header: "结果",
      cell: (item) =>
        item.status === "succeeded" ? (
          <StatusTag tone="neutral" icon={<CircleCheck size={14} />}>
            成功
          </StatusTag>
        ) : (
          <StatusTag tone="outline" icon={<CircleX size={14} />}>
            失败
          </StatusTag>
        ),
    },
    { id: "revision", header: "Revision", cell: (item) => item.revision ?? UNKNOWN_TIME, numeric: true },
    { id: "error", header: "错误码", cell: (item) => (item.error_code === undefined ? UNKNOWN_TIME : <code>{item.error_code}</code>) },
  ];

  return (
    <Panel title="目录操作记录">
      <QueryContent query={operations} loading={<Skeleton label="正在加载目录操作记录" lines={3} />}>
        {(items) =>
          items.length === 0 ? (
            <p>还没有目录操作记录。</p>
          ) : (
            <>
              <DataTable caption="目录操作记录" columns={columns} rows={items} rowKey={(item) => item.id} />
              {items.length >= OPERATION_LIMIT ? <p className={styles.note}>只显示最近 {OPERATION_LIMIT} 条记录。</p> : null}
            </>
          )
        }
      </QueryContent>
    </Panel>
  );
}
