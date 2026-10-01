import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DataTable, type Column } from "./data-table";

type Row = { id: string; name: string; count: number };
const rows: Row[] = [
  { id: "a", name: "classical-a", count: 3 },
  { id: "b", name: "classical-b", count: 12 },
];
const columns: Column<Row>[] = [
  { id: "name", header: "目录", cell: (row) => row.name, rowHeader: true },
  { id: "count", header: "数量", cell: (row) => row.count, numeric: true },
  { id: "action", header: "操作", cell: (row) => <button type="button">处理 {row.name}</button>, hideLabelWhenStacked: true },
];

describe("DataTable", () => {
  it("用 caption 作为表格名称，列标题与行标题有对应语义", () => {
    render(<DataTable caption="目录列表" columns={columns} rows={rows} rowKey={(row) => row.id} />);
    const table = screen.getByRole("table", { name: "目录列表" });
    expect(within(table).getAllByRole("columnheader").map((header) => header.textContent)).toEqual(["目录", "数量", "操作"]);
    expect(within(table).getAllByRole("rowheader").map((header) => header.textContent)).toEqual(["classical-a", "classical-b"]);
    expect(within(table).getAllByRole("row")).toHaveLength(3);
  });

  it("每个值都带标签（窄屏堆叠时显示），只有按钮的列不带", () => {
    render(<DataTable caption="目录列表" columns={columns} rows={rows} rowKey={(row) => row.id} />);
    const cells = screen.getAllByRole("cell");
    expect(cells[0]?.getAttribute("data-label")).toBe("数量");
    expect(cells[1]?.getAttribute("data-label")).toBeNull();
    expect(within(cells[1] as HTMLElement).getByRole("button", { name: "处理 classical-a" })).toBeTruthy();
  });

  it("按行标识生成行，行内容与数据一一对应", () => {
    render(<DataTable caption="目录列表" columns={columns} rows={rows} rowKey={(row) => row.id} />);
    const dataRows = screen.getAllByRole("row").slice(1);
    expect(within(dataRows[1] as HTMLElement).getByText("12")).toBeTruthy();
  });
});
