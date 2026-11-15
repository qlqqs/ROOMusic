import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FilterField, type FilterCommitMode } from "./filter-field";

// 用假定时器精确推进 250ms 的停顿；输入用 fireEvent（user-event 自带的延迟会和假定时器纠缠）。
beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

const normalize = (text: string) => (text.trim() === "" ? undefined : text.trim());
const validate = (text: string) => (text.length > 10 ? "太长了" : null);

/** 地址的替身：value 是“地址里的值”，每次写入记下方式；setValue 模拟后退、前进或别处改了地址。 */
function setup(initial?: string, options: { lag?: number } = {}) {
  const commits: { value: string | undefined; mode: FilterCommitMode }[] = [];
  let setValue: (value: string | undefined) => void = () => undefined;
  function Harness() {
    const [value, set] = useState(initial);
    setValue = set;
    return (
      <>
        <FilterField
          label="筛选专辑"
          placeholder="在专辑中筛选"
          value={value}
          normalize={normalize}
          validate={validate}
          onCommit={(next, mode) => {
            commits.push({ value: next, mode });
            // lag：像路由那样过一会儿地址才变。
            if (options.lag === undefined) set(next);
            else window.setTimeout(() => set(next), options.lag);
          }}
        />
        <button type="button">别处</button>
      </>
    );
  }
  render(<Harness />);
  const input = () => screen.getByRole<HTMLInputElement>("textbox", { name: "筛选专辑" });
  const type = (text: string) => fireEvent.change(input(), { target: { value: text } });
  const wait = (ms: number) => act(() => vi.advanceTimersByTime(ms));
  return { commits, input, type, wait, setValue: (value: string | undefined) => act(() => setValue(value)) };
}

describe("FilterField", () => {
  it("停止输入 250ms 之后才写入；连续输入只写最后的值", () => {
    const { commits, input, type, wait } = setup();
    expect(input().placeholder).toBe("在专辑中筛选");
    type("A");
    wait(200);
    type("Al");
    wait(249);
    expect(commits).toEqual([]);
    wait(1);
    expect(commits).toEqual([{ value: "Al", mode: "push" }]);
  });

  it("一次输入过程：第一次写入新增历史，之后替换", () => {
    const { commits, type, wait } = setup();
    type("A");
    wait(250);
    type("Al");
    wait(250);
    type("Alb");
    wait(250);
    expect(commits.map((commit) => commit.mode)).toEqual(["push", "replace", "replace"]);
  });

  it("离开输入框：还在等待的输入立即写入，之后的输入是新的过程", () => {
    const { commits, input, type, wait } = setup();
    type("A");
    fireEvent.blur(input());
    expect(commits).toEqual([{ value: "A", mode: "push" }]);
    type("AB");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "AB", mode: "push" });
  });

  it("回车立即写入；与地址相同的值不再写", () => {
    const { commits, input, type } = setup();
    type("Album ");
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(commits).toEqual([{ value: "Album", mode: "push" }]);
    // 尾随空格整理之后与地址相同：不新增也不替换；框里的文字保留原样。
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(commits).toHaveLength(1);
    expect(input().value).toBe("Album ");
  });

  it("× 与 Esc 清除：框清空、焦点留在框里、地址去掉筛选词；之后的输入是新的过程", () => {
    const { commits, input, type, wait } = setup("Album");
    expect(input().value).toBe("Album");
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(input().value).toBe("");
    expect(document.activeElement).toBe(input());
    expect(commits).toEqual([{ value: undefined, mode: "push" }]);
    expect(screen.queryByRole("button", { name: "清除筛选" })).toBeNull();

    type("B");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "B", mode: "push" });
    type("Bc");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "Bc", mode: "replace" });
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(input().value).toBe("");
    // 这次过程里已经新增过一条：清除替换它。
    expect(commits.at(-1)).toEqual({ value: undefined, mode: "replace" });
    type("C");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "C", mode: "push" });
  });

  it("打字删光等于清空：这次过程结束，下一次输入重新新增历史", () => {
    const { commits, type, wait } = setup();
    type("A");
    wait(250);
    type("");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: undefined, mode: "replace" });
    type("B");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "B", mode: "push" });
  });

  it("框里没有字时 Esc 不做什么", () => {
    const { commits, input } = setup();
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(commits).toEqual([]);
  });

  it("不合格的输入不写入、不静默丢弃：说明显示在框下方并与输入框关联，改好之后消失", () => {
    const { commits, input, type, wait } = setup();
    type("12345678901");
    wait(250);
    expect(commits).toEqual([]);
    const message = screen.getByText("太长了");
    expect(input().getAttribute("aria-invalid")).toBe("true");
    expect(input().getAttribute("aria-describedby")).toBe(message.parentElement?.id);
    type("123");
    wait(250);
    expect(commits).toEqual([{ value: "123", mode: "push" }]);
    expect(screen.queryByText("太长了")).toBeNull();
    expect(input().hasAttribute("aria-invalid")).toBe(false);
  });

  it("地址被别处改了（后退、前进）：框回到地址里的值，下一次输入是新的过程", () => {
    const { commits, input, type, wait, setValue } = setup();
    type("A");
    wait(250);
    setValue("Other");
    expect(input().value).toBe("Other");
    type("Otherx");
    wait(250);
    expect(commits.at(-1)).toEqual({ value: "Otherx", mode: "push" });
  });

  it("等待期间地址被别处改了：这次输入作废，不覆盖别处的修改", () => {
    const { commits, input, type, wait, setValue } = setup();
    type("A");
    wait(100);
    setValue("Other");
    wait(500);
    expect(commits).toEqual([]);
    expect(input().value).toBe("Other");
  });

  it("自己写入的值回到地址时框里的文字不被重置（尾随空格保留）", () => {
    const { commits, input, type, wait } = setup();
    type("Album ");
    wait(250);
    expect(commits).toEqual([{ value: "Album", mode: "push" }]);
    expect(input().value).toBe("Album ");
  });

  it("刚写入、地址还没跟上时清除：仍然清掉刚写入的值，地址跟上之后框保持为空", () => {
    const { commits, input, type, wait } = setup(undefined, { lag: 50 });
    type("A");
    fireEvent.blur(input());
    expect(commits).toEqual([{ value: "A", mode: "push" }]);
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(commits.at(-1)).toEqual({ value: undefined, mode: "push" });
    wait(100);
    expect(input().value).toBe("");
  });
});
