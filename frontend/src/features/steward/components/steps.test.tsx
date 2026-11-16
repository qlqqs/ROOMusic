import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { NATIVE_SEARCH, type StepItem } from "../run/reduce";
import { Citations } from "./sources";
import { Steps } from "./steps";

function item(callId: string, name: string, overrides: Partial<StepItem> = {}): StepItem {
  return { callId, name, input: {}, status: "succeeded", output: {}, errorCode: null, durationMs: 1_000, executed: true, textsBefore: 0, ...overrides };
}

describe("过程摘要", () => {
  it("只有只读工具：查了 N 处资料", () => {
    render(<Steps items={[item("c1", "list_albums"), item("c2", "get_playback")]} live={false} />);
    expect(screen.getByText("查了 2 处资料，用时 2 秒")).toBeTruthy();
  });

  it("执行过改动类工具：做了 N 步", () => {
    render(<Steps items={[item("c1", "get_playback"), item("c2", "control_playback", { durationMs: null })]} live={false} />);
    expect(screen.getByText("做了 2 步，用时 1 秒")).toBeTruthy();
  });

  it("改动类工具没有执行（token_limit）时仍是查了 N 处资料", () => {
    render(<Steps items={[item("c1", "list_playlists"), item("c2", "create_playlist", { status: "failed", errorCode: "token_limit", durationMs: null, executed: false })]} live={false} />);
    expect(screen.getByText("查了 1 处资料，用时 1 秒")).toBeTruthy();
  });
});

describe("联网搜索的条目与来源（C8）", () => {
  it("搜索条目列出结果的标题与域名；标题为空时用域名，只有 http、https 是链接；标题按文字显示", () => {
    const output = { results: [{ title: "<b>Bayreuth</b>", url: "https://www.example.org/a?x=1" }, { title: "", url: "https://b.example.com/" }, { title: "危险", url: "javascript:alert(1)" }] };
    render(<Steps items={[item("s1", NATIVE_SEARCH, { input: { q: "贝九 1951" }, output, durationMs: null })]} live={true} />);
    expect(screen.getByText("联网搜索（模型自带）")).toBeTruthy();
    expect(screen.getByText("贝九 1951")).toBeTruthy();
    const list = screen.getByRole("list", { name: "联网搜索（模型自带）的结果" });
    const links = Array.from(list.querySelectorAll("a"));
    expect(links.map((link) => [link.textContent, link.getAttribute("href"), link.getAttribute("target"), link.getAttribute("rel")])).toEqual([
      ["<b>Bayreuth</b>", "https://www.example.org/a?x=1", "_blank", "noopener noreferrer"],
      ["b.example.com", "https://b.example.com/", "_blank", "noopener noreferrer"],
    ]);
    expect(list.textContent).toContain("example.org");
    expect(list.textContent).toContain("危险");
    expect(list.querySelector("b")).toBeNull();
  });

  it("搜索失败时写原因，不列结果", () => {
    render(<Steps items={[item("s2", "web_search", { input: { q: "q" }, status: "failed", errorCode: "search_quota_exceeded", output: { code: "search_quota_exceeded" } })]} live={true} />);
    expect(screen.getByText("搜索额度用完了")).toBeTruthy();
    expect(screen.queryByRole("list", { name: "联网搜索的结果" })).toBeNull();
  });

  it("回答下面的来源：没有可显示的来源时整块不出现", () => {
    const { rerender } = render(<Citations citations={[{ url: "https://example.org/a", title: "第一篇" }, { url: "https://c.example.net/", title: "" }]} />);
    const list = screen.getByRole("list", { name: "来源" });
    expect(Array.from(list.querySelectorAll("a")).map((link) => link.textContent)).toEqual(["第一篇", "c.example.net"]);
    rerender(<Citations citations={[{ url: "data:text/html,x", title: "" }]} />);
    expect(screen.queryByText("来源")).toBeNull();
  });
});
