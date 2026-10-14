import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, useEffect } from "react";
import { describe, expect, it, vi } from "vitest";
import { Disclosure } from "./disclosure";

function Probe({ onMount }: { onMount: () => void }) {
  useEffect(onMount, [onMount]);
  return <p>内容</p>;
}

describe("Disclosure", () => {
  it("默认折叠：内容不渲染也不挂载，所以没有可聚焦的隐藏控件，内容里的请求也不会发出", () => {
    const onMount = vi.fn();
    render(
      <Disclosure title="来源与证据" detail="（3 项）">
        <Probe onMount={onMount} />
      </Disclosure>,
    );
    expect(screen.getByText("来源与证据")).toBeTruthy();
    expect(screen.getByText("（3 项）")).toBeTruthy();
    expect(screen.queryByText("内容")).toBeNull();
    expect(onMount).not.toHaveBeenCalled();
  });

  it("点标题行展开、再点收起；每次展开都是新的挂载", async () => {
    const onMount = vi.fn();
    const user = userEvent.setup();
    render(
      <Disclosure title="修改记录">
        <Probe onMount={onMount} />
      </Disclosure>,
    );
    await user.click(screen.getByText("修改记录"));
    expect(screen.getByText("内容")).toBeTruthy();
    expect(onMount).toHaveBeenCalledTimes(1);
    await user.click(screen.getByText("修改记录"));
    expect(screen.queryByText("内容")).toBeNull();
    await user.click(screen.getByText("修改记录"));
    expect(onMount).toHaveBeenCalledTimes(2);
  });

  it("标题行是原生 summary，可以用 Tab 聚焦（jsdom 不模拟 Enter 展开 summary，由 Playwright 的键盘用例覆盖）", async () => {
    const user = userEvent.setup();
    render(<Disclosure title="修改记录">内容</Disclosure>);
    await user.tab();
    expect(document.activeElement?.tagName).toBe("SUMMARY");
  });

  it("summaryRef 指向标题行：区块外的操作结束后可以把焦点带回这里", () => {
    const ref = createRef<HTMLElement>();
    render(
      <Disclosure title="修改记录" summaryRef={ref}>
        内容
      </Disclosure>,
    );
    ref.current?.focus();
    expect(document.activeElement).toBe(ref.current);
    expect(ref.current?.tagName).toBe("SUMMARY");
  });
});
