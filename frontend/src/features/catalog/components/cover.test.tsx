import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Cover } from "./cover";

const artwork = { resource_id: "0f3c.png", mime: "image/png", width: 600, height: 400 };

function image(container: HTMLElement): HTMLImageElement {
  const found = container.querySelector("img");
  if (found === null) throw new Error("没有图片");
  return found;
}

describe("Cover", () => {
  it("有封面：从同源的封面接口加载，带接口给的宽高、懒加载与异步解码；卡片里是装饰", () => {
    const { container } = render(<Cover artwork={artwork} title="第九交响曲" variant="card" />);
    const img = image(container);
    expect(img.getAttribute("src")).toBe("/api/v1/artworks/0f3c.png");
    expect(img.getAttribute("width")).toBe("600");
    expect(img.getAttribute("height")).toBe("400");
    expect(img.getAttribute("loading")).toBe("lazy");
    expect(img.getAttribute("decoding")).toBe("async");
    expect(img.getAttribute("alt")).toBe("");
    // 整个封面对读屏隐藏：标题已经在卡片的链接名称里。
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("详情页的封面有替代文字，并且立即加载", () => {
    const { container } = render(<Cover artwork={artwork} title="第九交响曲" variant="detail" />);
    expect(screen.getByRole("img", { name: "《第九交响曲》封面" })).toBe(image(container));
    expect(image(container).getAttribute("loading")).toBe("eager");
  });

  it("加载中、已加载：状态随图片事件变化，版面尺寸不变", () => {
    const { container } = render(<Cover artwork={artwork} title="A" variant="card" />);
    const frame = () => container.querySelector("[data-state]");
    expect(frame()?.getAttribute("data-state")).toBe("loading");
    fireEvent.load(image(container));
    expect(frame()?.getAttribute("data-state")).toBe("loaded");
    expect(image(container)).toBeTruthy();
  });

  it("没有封面：确定性的文字版面，不发图片请求；卡片里是装饰，详情页有名称", () => {
    const card = render(<Cover artwork={null} title="没有封面的专辑" variant="card" />);
    expect(card.container.querySelector("img")).toBeNull();
    expect(card.container.textContent).toContain("没有封面的专辑");
    expect(card.container.querySelector('[data-state="none"]')).not.toBeNull();
    card.unmount();

    render(<Cover artwork={null} title="没有封面的专辑" variant="detail" />);
    expect(screen.getByRole("img", { name: "《没有封面的专辑》没有封面" })).toBeTruthy();
  });

  it("卡片里加载失败：同样的版面加“封面无法加载”，没有重新加载按钮", () => {
    const { container } = render(<Cover artwork={artwork} title="失败的专辑" variant="card" />);
    fireEvent.error(image(container));
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector('[data-state="failed"]')?.textContent).toContain("封面无法加载");
    expect(container.textContent).toContain("失败的专辑");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("详情页加载失败：说明原因，“重新加载封面”重新挂载图片并再次请求", async () => {
    const { container } = render(<Cover artwork={artwork} title="失败的专辑" variant="detail" />);
    const first = image(container);
    fireEvent.error(first);
    expect(screen.getByText("封面无法加载")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "重新加载封面" }));
    const second = image(container);
    expect(second).not.toBe(first); // 新的图片元素：浏览器会重新发请求
    expect(second.getAttribute("src")).toBe("/api/v1/artworks/0f3c.png");
    expect(container.querySelector('[data-state="loading"]')).not.toBeNull();
    expect(screen.queryByRole("button", { name: "重新加载封面" })).toBeNull();

    fireEvent.load(second);
    expect(container.querySelector('[data-state="loaded"]')).not.toBeNull();
  });

  it("换了封面：状态回到加载中，不沿用上一张的失败", () => {
    const { container, rerender } = render(<Cover artwork={artwork} title="A" variant="card" />);
    fireEvent.error(image(container));
    expect(container.querySelector('[data-state="failed"]')).not.toBeNull();
    rerender(<Cover artwork={{ ...artwork, resource_id: "other.png" }} title="A" variant="card" />);
    expect(container.querySelector('[data-state="loading"]')).not.toBeNull();
    expect(image(container).getAttribute("src")).toBe("/api/v1/artworks/other.png");
  });
});
