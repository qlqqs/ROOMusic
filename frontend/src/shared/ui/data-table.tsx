import type { ReactNode } from "react";
import { cx } from "../lib/class-names";
import styles from "./data-table.module.css";

export type Column<Row> = {
  id: string;
  /** 列标题；窄屏堆叠成逐行卡片时，它是每个值前面的标签。 */
  header: string;
  cell: (row: Row) => ReactNode;
  /** 这一列是行的名称，渲染为行标题（th scope="row"）；窄屏堆叠时作为该行的标题。 */
  rowHeader?: boolean;
  /** 数字列：右对齐，使用等高等宽数字。 */
  numeric?: boolean;
  /** 窄屏堆叠时不显示标签（例如只有按钮的操作列）。 */
  hideLabelWhenStacked?: boolean;
};

type DataTableProps<Row> = {
  /** 表格的可访问名称，不显示；区块标题已经可见时仍需要它，让读屏用户从表格导航里认出这张表。 */
  caption: string;
  columns: readonly Column<Row>[];
  rows: readonly Row[];
  /** 行的稳定标识（后端 ID），不用下标或显示内容。 */
  rowKey: (row: Row) => string;
};

/**
 * 紧凑数据表。宽度由容器决定而不是视口：容器不足 640px（窄屏，或带侧栏的中等视口）时每行堆叠成“标签：值”，
 * 不产生整页或局部的横向滚动。显式写出 ARIA 角色，是因为 Safari 在把表格元素改成块级显示后会丢掉表格语义。
 * 空列表由调用方显示空态，不渲染只有表头的空表。
 */
export function DataTable<Row>({ caption, columns, rows, rowKey }: DataTableProps<Row>) {
  return (
    <div className={styles.container}>
      <table className={styles.table} role="table">
        <caption className={styles.caption}>{caption}</caption>
        <thead role="rowgroup">
          <tr role="row">
            {columns.map((column) => (
              <th key={column.id} scope="col" role="columnheader" className={cx(column.numeric && styles.numeric)}>
                {column.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody role="rowgroup">
          {rows.map((row) => (
            <tr key={rowKey(row)} role="row">
              {columns.map((column) => {
                const content = <div className={styles.value}>{column.cell(row)}</div>;
                if (column.rowHeader) {
                  return (
                    <th key={column.id} scope="row" role="rowheader" className={styles.rowHeader}>
                      {content}
                    </th>
                  );
                }
                return (
                  <td
                    key={column.id}
                    role="cell"
                    data-label={column.hideLabelWhenStacked ? undefined : column.header}
                    className={cx(column.numeric && styles.numeric, column.hideLabelWhenStacked && styles.unlabeled)}
                  >
                    {content}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
