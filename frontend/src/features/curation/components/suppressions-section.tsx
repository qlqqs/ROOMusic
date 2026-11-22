import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, ConfirmDialog, DataTable, FailureNotice, Pagination, QueryContent, SettingsGroup, Skeleton, VisuallyHidden, type Column } from "../../../shared/ui";
import type { SuppressionDTO } from "../api/decoders";
import { isPairedIdentityMarker, suppressionSubjectText, suppressionTargetText, suppressionValueText } from "../curation-text";
import { useClearSuppression, useSuppressions } from "../hooks/use-curation";
import styles from "./curation-sections.module.css";

/**
 * “不再提出”卡片：撤销过的自动修改留下的标记，最新的在前；清除一条之后后台可以再次写入同一个值。
 * 封面与歌词各留两条（按内容、按外部编号），清除时一起清除，所以列表只显示按内容的那一条。值是外部来的不可信文本。
 * 目标是发行的行链接到这个发行的操作记录。页码来自 URL（marker_page）。
 */
export function SuppressionsSection({ page }: { page: number }) {
  const suppressions = useSuppressions(page);
  const clear = useClearSuppression();
  const navigate = useNavigate();
  const [target, setTarget] = useState<SuppressionDTO | null>(null);

  const pagination = suppressions.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !suppressions.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/settings/curation", search: (previous) => ({ ...previous, marker_page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  const columns: Column<SuppressionDTO>[] = [
    { id: "created", header: "时间", rowHeader: true, cell: (item) => formatDateTime(item.created_at) },
    {
      id: "target",
      header: "对象",
      cell: (item) =>
        item.target_type === "release" ? (
          <Link to="/settings/operations" search={{ release: item.target_id }}>
            {suppressionTargetText(item)}
            <VisuallyHidden>的操作记录</VisuallyHidden>
          </Link>
        ) : (
          suppressionTargetText(item)
        ),
    },
    { id: "subject", header: "类别", cell: (item) => suppressionSubjectText(item) },
    { id: "value", header: "值", cell: (item) => <span className={styles.untrusted}>{suppressionValueText(item)}</span> },
    {
      id: "actions",
      header: "操作",
      hideLabelWhenStacked: true,
      cell: (item) => (
        <Button onClick={() => setTarget(item)}>
          清除
          <VisuallyHidden>
            {formatDateTime(item.created_at)} 的{suppressionSubjectText(item)}标记
          </VisuallyHidden>
        </Button>
      ),
    },
  ];

  const data = suppressions.data;
  const listed = data !== undefined && !outOfRange && data.pagination.total > 0 ? data : null;
  return (
    <SettingsGroup
      title="不再提出"
      footer={
        listed === null ? null : (
          <Pagination
            label="不再提出分页"
            page={listed.pagination.page}
            pageCount={pageCountOf(listed.pagination.total, listed.pagination.page_size)}
            renderLink={(targetPage, props) => (
              <Link to="/settings/curation" search={(previous) => ({ ...previous, marker_page: targetPage > 1 ? targetPage : undefined })} {...props} />
            )}
          />
        )
      }
    >
      <p className={styles.groupNote}>撤销过的自动修改不会再自动出现。清除一条，后台可以再次写入同一个值。</p>
      <QueryContent query={suppressions} loading={<Skeleton label="正在加载标记" lines={3} />}>
        {(page) => {
          const rows = page.items.filter((item) => !isPairedIdentityMarker(item));
          return outOfRange ? (
            <Skeleton label="正在加载标记" lines={3} />
          ) : page.pagination.total === 0 ? (
            <p>没有标记。</p>
          ) : (
            <div aria-busy={suppressions.isPlaceholderData}>
              <DataTable caption="不再提出的标记" columns={columns} rows={rows} rowKey={(item) => item.id} />
            </div>
          );
        }}
      </QueryContent>
      <ConfirmDialog
        open={target !== null}
        title="清除这条标记？"
        confirmLabel="清除"
        pending={clear.isPending}
        error={clear.isError ? <FailureNotice error={clear.error} context="write" /> : undefined}
        onCancel={() => {
          setTarget(null);
          clear.reset();
        }}
        onConfirm={() => {
          if (target === null) return;
          clear.mutate(target.id, { onSuccess: () => setTarget(null) });
        }}
      >
        {target === null ? null : (
          <p>
            清除之后，后台可以再次把{suppressionTargetText(target)}的{suppressionSubjectText(target)}写成“<span className={styles.untrusted}>{suppressionValueText(target)}</span>”。
          </p>
        )}
      </ConfirmDialog>
    </SettingsGroup>
  );
}
