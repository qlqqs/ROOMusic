import { MoreHorizontal } from "lucide-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { DataTable, MenuButton, Pagination, QueryContent, SettingsGroup, Skeleton, type Column } from "../../../shared/ui";
import type { CurationBatchDTO } from "../api/decoders";
import { useCurationBatches } from "../hooks/use-curation";
import { useScopeRevert } from "../hooks/use-scope-revert";
import { ScopeRevertDialog, ScopeRevertResult, type ScopeRevertTarget } from "./scope-revert-dialog";
import styles from "./curation-sections.module.css";

/**
 * “批次”卡片：后台的一段连续工作是一批，最新的在前；每行写开始时间、改动数与已撤销数，行尾菜单查看这一批的改动（操作记录按批次筛选）或撤销这一批。
 * 页码来自 URL（batch_page）：越界时用替换历史的方式回到末页。模型层上线之前所有批次都是程序层的，所以不显示“层”这一列。
 */
export function CurationBatchesSection({ page, refresh }: { page: number; refresh: () => void }) {
  const batches = useCurationBatches(page);
  const revert = useScopeRevert(refresh);
  const navigate = useNavigate();
  const [target, setTarget] = useState<ScopeRevertTarget | null>(null);

  const pagination = batches.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !batches.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/settings/curation", search: (previous) => ({ ...previous, batch_page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  const askRevert = (batch: CurationBatchDTO) => {
    revert.reset();
    setTarget({ scope: { kind: "batch", id: batch.id }, openedAt: batch.opened_at, remaining: batch.operations - batch.reverted, ongoing: batch.closed_at === null });
  };

  const columns: Column<CurationBatchDTO>[] = [
    { id: "opened", header: "开始时间", rowHeader: true, cell: (batch) => formatDateTime(batch.opened_at) },
    { id: "state", header: "状态", cell: (batch) => (batch.closed_at === null ? "进行中" : batch.reverted >= batch.operations && batch.operations > 0 ? "已全部撤销" : "已结束") },
    { id: "operations", header: "改动", numeric: true, cell: (batch) => formatCount(batch.operations) },
    { id: "reverted", header: "已撤销", numeric: true, cell: (batch) => formatCount(batch.reverted) },
    {
      id: "actions",
      header: "操作",
      hideLabelWhenStacked: true,
      cell: (batch) => (
        <MenuButton
          label={`${formatDateTime(batch.opened_at)} 开始的批次的操作`}
          iconOnly
          icon={<MoreHorizontal size={18} />}
          actions={[
            { id: "view", label: "查看这一批的改动", onSelect: () => void navigate({ to: "/settings/operations", search: { batch: batch.id } }) },
            {
              id: "revert",
              label: "撤销这一批",
              danger: true,
              disabled: batch.operations === 0 || batch.reverted >= batch.operations,
              onSelect: () => askRevert(batch),
            },
          ]}
        />
      ),
    },
  ];

  const data = batches.data;
  const listed = data !== undefined && !outOfRange && data.pagination.total > 0 ? data : null;
  return (
    <SettingsGroup
      title="批次"
      footer={
        <>
          <ScopeRevertResult state={revert.state} onDismiss={revert.reset} />
          <div className={styles.footerRow}>
            <Link to="/settings/operations" search={{ source: "curation" }}>
              查看全部自动修改
            </Link>
            {listed === null ? null : (
              <Pagination
                label="批次分页"
                page={listed.pagination.page}
                pageCount={pageCountOf(listed.pagination.total, listed.pagination.page_size)}
                renderLink={(targetPage, props) => (
                  <Link to="/settings/curation" search={(previous) => ({ ...previous, batch_page: targetPage > 1 ? targetPage : undefined })} {...props} />
                )}
              />
            )}
          </div>
        </>
      }
    >
      <QueryContent query={batches} loading={<Skeleton label="正在加载批次" lines={3} />}>
        {(page) =>
          outOfRange ? (
            <Skeleton label="正在加载批次" lines={3} />
          ) : page.pagination.total === 0 ? (
            <p>还没有自动整理的记录。</p>
          ) : (
            <div aria-busy={batches.isPlaceholderData}>
              <DataTable caption="自动整理的批次" columns={columns} rows={page.items} rowKey={(batch) => batch.id} />
            </div>
          )
        }
      </QueryContent>
      <ScopeRevertDialog
        target={target}
        state={revert.state}
        onConfirm={() => {
          if (target !== null) revert.start(target.scope);
        }}
        onResume={revert.resume}
        onCancel={() => {
          setTarget(null);
          if (revert.state.phase !== "running") revert.reset();
        }}
      />
    </SettingsGroup>
  );
}
