import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Answer, safeHref } from "./answer";

describe("回答的 Markdown", () => {
  it("表格、列表、加粗与代码正常显示，表格单元格不带对齐样式", () => {
    const { container } = render(
      <Answer
        text={["一共 **3 版**：", "", "| 版本 | 年份 |", "| :--- | ---: |", "| 拜罗伊特 | 1951 |", "", "- 柏林 1942", "- 卢塞恩 1954", "", "用 `get_album` 查的。"].join("\n")}
      />,
    );
    expect(screen.getByText("3 版").tagName).toBe("STRONG");
    const table = screen.getByRole("table");
    expect(within(table).getByRole("columnheader", { name: "年份" }).getAttribute("style")).toBeNull();
    expect(within(table).getByRole("cell", { name: "1951" }).getAttribute("style")).toBeNull();
    expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual(["柏林 1942", "卢塞恩 1954"]);
    expect(screen.getByText("get_album").tagName).toBe("CODE");
    expect(container.querySelector("script, iframe")).toBeNull();
  });

  it("HTML 标签按文字显示，不生成元素", () => {
    const { container } = render(<Answer text={'看这里 <b>加粗</b> <img src="x" onerror="alert(1)"> <script>alert(1)</script>'} />);
    expect(container.querySelector("b, img, script")).toBeNull();
    expect(container.textContent).toContain("<b>加粗</b>");
    expect(container.textContent).toContain("<script>alert(1)</script>");
  });

  it("只有 http、https 链接可点，在新标签页打开；javascript: 与相对地址只显示文字", () => {
    render(<Answer text={"[官网](https://example.com/a) [坏](javascript:alert(1)) [站内](/albums) [邮件](mailto:a@example.com)"} />);
    const link = screen.getByRole("link", { name: "官网" });
    expect(link.getAttribute("href")).toBe("https://example.com/a");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noreferrer noopener");
    expect(screen.getAllByRole("link")).toHaveLength(1);
    expect(screen.getByText("坏").closest("a")).toBeNull();
    expect(screen.getByText("站内").closest("a")).toBeNull();
  });

  it("图片不加载，显示替代文字", () => {
    const { container } = render(<Answer text={"![封面](https://example.com/cover.jpg) ![](https://example.com/x.png)"} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("［图片：封面］");
    expect(container.textContent).toContain("［图片］");
  });

  it("safeHref 只放行 http 与 https", () => {
    expect(safeHref("http://a.example/")).toBe("http://a.example/");
    expect(safeHref("HTTPS://A.example/x")).toBe("https://a.example/x");
    expect(safeHref("javascript:alert(1)")).toBeNull();
    expect(safeHref("data:text/html,x")).toBeNull();
    expect(safeHref("")).toBeNull();
    expect(safeHref(undefined)).toBeNull();
  });
});
