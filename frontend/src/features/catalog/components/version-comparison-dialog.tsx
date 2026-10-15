import { Link } from "@tanstack/react-router";
import { useId, useRef, type RefObject } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Button, ModalDialog, Notice, QueryContent, Skeleton, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseComparisonDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { comparisonRows, versionLabel } from "../comparison";
import { useReleaseComparison } from "../hooks/use-release-comparison";
import { ComparisonTable } from "./comparison-table";
import styles from "./version-comparison-dialog.module.css";

type VersionComparisonDialogProps = {
  open: boolean;
  groupId: string;
  /** 从列表带进来的筛选与页码：从列头切换版本时随链接保留，“返回专辑列表”仍然有效。 */
  list: AlbumSearch;
  onClose: () => void;
};

type ContentProps = Pick<VersionComparisonDialogProps, "groupId" | "list" | "onClose"> & {
  titleId: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
};

function ComparisonBody({ comparison, groupId, list, onClose }: Pick<ContentProps, "groupId" | "list" | "onClose"> & { comparison: ReleaseComparisonDTO }) {
  const { releases } = comparison;
  // 对话框打开期间别人重新扫描、版本只剩一个：没有可以对比的内容，如实说明。
  if (releases.length < 2) {
    return <Notice tone="info" title="现在只有一个可见版本">这张专辑刚发生了变化，没有可以对比的版本。关闭后页面会刷新版本列表。</Notice>;
  }
  const rows = comparisonRows(releases);
  const first = releases[0];
  return (
    <>
      <p className={styles.lead}>
        以{versionLabel(0)} 为基准：与它不同的内容用金色下划线标出，没有记录的写“—”。
        {first?.id === comparison.default_release_id ? `${versionLabel(0)} 是这张专辑的默认版本。` : ""}这里只如实列出各版本的信息，不做优劣判断。
      </p>
      <ComparisonTable
        releases={releases}
        rows={rows}
        defaultReleaseId={comparison.default_release_id}
        renderViewLink={(release, index) => (
          <Link
            to="/albums/$groupId"
            params={{ groupId }}
            // 第一个版本是代表版本：它的链接不带 release 参数，与版本导航一致。
            search={{ release: index === 0 ? undefined : release.id }}
            state={{ albumList: list }}
            onClick={onClose}
          >
            查看这个版本
            <VisuallyHidden>（{versionLabel(index)}）</VisuallyHidden>
          </Link>
        )}
      />
      {comparison.release_count > releases.length ? (
        <p className={styles.note}>
          共 {formatCount(comparison.release_count)} 个版本，这里只比较前 {formatCount(releases.length)} 个。
        </p>
      ) : null}
    </>
  );
}

/** 对话框的内容：只在打开时挂载，所以对比数据在打开之后才读取，关闭即丢弃。 */
function ComparisonContent({ groupId, list, onClose, titleId, headingRef }: ContentProps) {
  const comparison = useReleaseComparison(groupId);
  return (
    <div className={styles.content}>
      <header className={styles.header}>
        <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
          对比版本
        </h2>
        <Button onClick={onClose}>关闭</Button>
      </header>
      <div className={styles.body}>
        <QueryContent query={comparison} loading={<Skeleton label="正在读取版本对比" lines={6} />}>
          {(data) => <ComparisonBody comparison={data} groupId={groupId} list={list} onClose={onClose} />}
        </QueryContent>
      </div>
    </div>
  );
}

/**
 * 版本对比对话框（所有登录成员）：逐项并排比较这张专辑各个版本的发行字段与内容。尺寸按设计依据：宽度 min(980px, 100%)，
 * 最高 90vh，窄屏时在对话框内部滚动，标题行与“关闭”始终可见；关闭后焦点回到打开它的按钮；列头的“查看这个版本”关闭对话框并切换到该版本。
 */
export function VersionComparisonDialog({ open, groupId, list, onClose }: VersionComparisonDialogProps) {
  const titleId = useId();
  // 初始焦点在标题上：内容随加载变化，标题是稳定的落点。
  const headingRef = useRef<HTMLHeadingElement>(null);
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={onClose}>
      <ComparisonContent groupId={groupId} list={list} onClose={onClose} titleId={titleId} headingRef={headingRef} />
    </ModalDialog>
  );
}
