import type { ReactNode } from "react";
import { VisuallyHidden } from "../../../shared/ui";
import type { ComparedReleaseDTO } from "../api/decoders";
import { versionLabel, type ComparisonCell, type ComparisonRow } from "../comparison";
import styles from "./comparison-table.module.css";
import { DefaultVersionTag } from "./default-version-tag";
import { ManualCorrectionTag } from "./manual-correction-tag";

type ComparisonTableProps = {
  releases: readonly ComparedReleaseDTO[];
  rows: readonly ComparisonRow[];
  /** 生效中的默认版本（接口的 default_release_id）；自动选择时为 null，没有任何列带标签。 */
  defaultReleaseId: string | null;
  /** 列头里“查看这个版本”的链接，由调用方生成（通常是路由 Link，组件不依赖路由）。 */
  renderViewLink: (release: ComparedReleaseDTO, index: number) => ReactNode;
};

/** 一个格的内容。与基准列不同的值加下划线并带读屏文字（不只靠颜色）；没有值写“—”；人工修正带标记。 */
function CellValue({ cell }: { cell: ComparisonCell }) {
  const empty = cell.lines.length === 0;
  const lines = empty ? null : cell.lines.length === 1 ? <span className={styles.text}>{cell.lines[0]}</span> : (
    <ul className={styles.lines} role="list">
      {cell.lines.map((line) => (
        <li key={line}>
          <span className={styles.text}>{line}</span>
        </li>
      ))}
    </ul>
  );
  return (
    <div className={styles.value} data-differs={cell.differs ? "true" : undefined}>
      {empty ? (
        <>
          <span className={styles.text} aria-hidden="true">
            —
          </span>
          <VisuallyHidden>没有记录</VisuallyHidden>
        </>
      ) : (
        lines
      )}
      {cell.corrected ? (
        <>
          {" "}
          <ManualCorrectionTag />
        </>
      ) : null}
      {cell.differs ? <VisuallyHidden>（与{versionLabel(0)} 不同）</VisuallyHidden> : null}
    </div>
  );
}

/**
 * “字段 × 版本”的对比表：行标题是字段，列是各个版本，以第一列为基准。宽度由容器决定：窄于 640px 时每个字段堆叠成
 * “版本 N：值”的小段，不产生横向滚动，列头（版本名与“查看这个版本”）仍显示在表格最上面。
 * 显式写出 ARIA 角色，是因为 Safari 在把表格元素改成块级显示后会丢掉表格语义（与 DataTable 相同）。
 */
export function ComparisonTable({ releases, rows, defaultReleaseId, renderViewLink }: ComparisonTableProps) {
  return (
    <div className={styles.container}>
      <table className={styles.table} role="table">
        <caption className={styles.caption}>各个版本的对比</caption>
        <thead role="rowgroup">
          <tr role="row">
            <th scope="col" role="columnheader" className={styles.corner}>
              <VisuallyHidden>项目</VisuallyHidden>
            </th>
            {releases.map((release, index) => (
              <th key={release.id} scope="col" role="columnheader" className={styles.version}>
                <span className={styles.versionName}>{versionLabel(index)}</span>
                {release.id === defaultReleaseId ? (
                  <>
                    {" "}
                    <DefaultVersionTag />
                  </>
                ) : null}
                <span className={styles.viewLink}>{renderViewLink(release, index)}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody role="rowgroup">
          {rows.map((row) => (
            <tr key={row.key} role="row">
              <th scope="row" role="rowheader" className={styles.rowHeader}>
                {row.label}
              </th>
              {row.cells.map((cell, index) => (
                <td key={releases[index]?.id ?? index} role="cell" data-label={versionLabel(index)} className={styles.cell}>
                  <CellValue cell={cell} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
