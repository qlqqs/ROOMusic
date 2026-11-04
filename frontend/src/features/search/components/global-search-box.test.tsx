import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { deferred } from "../../../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { renderApp } from "../../../test/render-app";
import { decodeSearchSuggestions } from "../api/decoders";

const suggestionsPath = "/api/v1/search/suggestions";
const sample = decodeSearchSuggestions(sampleBody("search/suggestions"));

/** 两类建议刻意使用不同的目标，避免曲目误用专辑建议的跳转信息仍然通过。 */
function distinctSuggestions() {
  const data = structuredClone(sample);
  const album = data.albums.items[0];
  const track = data.tracks.items[0];
  if (album === undefined || track === undefined) throw new Error("搜索样本缺少专辑或曲目");
  track.group_id = "00000000-0000-4000-8000-000000000021";
  track.release.id = "00000000-0000-4000-8000-000000000022";
  track.match.release_id = track.release.id;
  data.albums.items = [album];
  data.tracks.items = [track];
  return { data, album, track };
}

async function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  api.on("GET", suggestionsPath, () => sampleResponse("search/suggestions"));
  const app = renderApp();
  await screen.findByRole("heading", { name: "概览", level: 1 });
  await waitFor(() => expect(api.requestsTo("GET", "/api/v1/catalog/status")).toHaveLength(1));
  const input = screen.getByRole("combobox", { name: "搜索" });
  vi.useFakeTimers();
  return { ...app, api, input, requests: () => api.requestsTo("GET", suggestionsPath) };
}

async function tick(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

function change(input: HTMLElement, value: string) {
  fireEvent.change(input, { target: { value } });
}

function selectedOption(input: HTMLElement) {
  const id = input.getAttribute("aria-activedescendant");
  return id === null ? null : document.getElementById(id);
}

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => undefined);
});

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe("全局搜索建议框", () => {
  it("最后一次输入满 250ms 才查询，单字爱可查，不请求结果总数", async () => {
    const { input, requests, api } = await setup();
    change(input, "周");
    await tick(200);
    change(input, " 爱 ");
    await tick(249);
    expect(requests()).toHaveLength(0);
    await tick(1);
    expect(requests()).toHaveLength(1);
    expect(new URL(requests()[0]?.path ?? "", "http://localhost").searchParams.get("q")).toBe("爱");
    expect(new URL(requests()[0]?.path ?? "", "http://localhost").searchParams.get("limit")).toBe("5");
    await tick(1);
    expect(screen.getAllByRole("option")).toHaveLength(4);
    expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(0);
  });

  it("IME 组合期间不查询或提交，结束后重新等待 250ms", async () => {
    const { input, requests, history } = await setup();
    change(input, "a");
    await tick(200);
    fireEvent.compositionStart(input);
    change(input, "爱");
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });
    await tick(1000);
    expect(requests()).toHaveLength(0);
    expect(history.location.pathname).toBe("/");
    fireEvent.compositionEnd(input, { data: "爱" });
    await tick(249);
    expect(requests()).toHaveLength(0);
    await tick(1);
    expect(requests()).toHaveLength(1);
  });

  it("上下键循环选择但焦点留在输入框，Esc 收起且不导航，再按下键可打开", async () => {
    const { input, history } = await setup();
    act(() => input.focus());
    change(input, "爱");
    await tick(250);
    await tick(1);
    expect(input.getAttribute("aria-expanded")).toBe("true");
    expect(input.getAttribute("aria-activedescendant")).toBeNull();
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(selectedOption(input)?.textContent).toContain("全部结果");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selectedOption(input)?.textContent).toContain("原版专辑");
    expect(selectedOption(input)?.getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selectedOption(input)?.textContent).toContain("Summer");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(selectedOption(input)?.textContent).toContain("原版专辑");
    expect(document.activeElement).toBe(input);
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input.getAttribute("aria-expanded")).toBe("false");
    expect(input.getAttribute("aria-activedescendant")).toBeNull();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(history.location.pathname).toBe("/");
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(screen.getByRole("listbox", { name: "搜索建议" })).toBeTruthy();
    expect(document.activeElement).toBe(input);
  });

  it.each(["未选建议", "查看全部"])("Enter（%s）进入保留原文的结果地址", async (mode) => {
    const { input, history } = await setup();
    change(input, " ＡＢＣ 愛 ");
    await tick(250);
    await tick(1);
    if (mode === "查看全部") fireEvent.keyDown(input, { key: "ArrowUp" });
    vi.useRealTimers();
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(history.location.pathname).toBe("/search"));
    expect(new URLSearchParams(history.location.search).get("q")).toBe("ＡＢＣ 愛");
  });

  it.each(["专辑", "曲目"])("Enter 打开选中%s的所在专辑与命中版本", async (kind) => {
    const { input, history, api } = await setup();
    const { data, album, track } = distinctSuggestions();
    expect(track.group_id).not.toBe(album.group.id);
    expect(track.release.id).not.toBe(album.matched_release_id);
    api.on("GET", suggestionsPath, () => jsonResponse(200, data));
    change(input, "爱");
    await tick(250);
    await tick(1);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    if (kind === "曲目") fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selectedOption(input)?.textContent).toContain(kind === "专辑" ? album.group.representative.title : track.title);
    vi.useRealTimers();
    fireEvent.keyDown(input, { key: "Enter" });
    const groupId = kind === "专辑" ? album.group.id : track.group_id;
    const releaseId = kind === "专辑" ? album.matched_release_id : track.release.id;
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${groupId}`));
    expect(new URLSearchParams(history.location.search).get("release")).toBe(releaseId);
  });

  it.each(["专辑", "曲目"])("%s建议显示作曲命中的完整原文，而不是查询词", async (kind) => {
    const { input, api } = await setup();
    const { data, album, track } = distinctSuggestions();
    data.query = "久石";
    const item = kind === "专辑" ? album : track;
    item.match = { ...item.match, field: "credit", role: "composer", kind: "prefix", original: "久石譲" };
    api.on("GET", suggestionsPath, () => jsonResponse(200, data));
    change(input, "久石");
    await tick(250);
    await tick(1);
    const option = within(screen.getByRole("group", { name: kind })).getByRole("option");
    expect(within(option).getByText("作曲：久石譲")).toBeTruthy();
    expect(within(option).queryByText("作曲：久石")).toBeNull();
    expect(option.textContent?.split("久石譲")).toHaveLength(2);
  });

  it.each(["专辑", "曲目"])("%s建议行内已显示作曲命中原文时不重复说明", async (kind) => {
    const { input, api } = await setup();
    const { data, album, track } = distinctSuggestions();
    data.query = "久石";
    const item = kind === "专辑" ? album : track;
    item.match = { ...item.match, field: "credit", role: "composer", kind: "prefix", original: "久石譲" };
    album.group.representative.album_artist = "久石譲";
    track.artist = "久石譲";
    api.on("GET", suggestionsPath, () => jsonResponse(200, data));
    change(input, "久石");
    await tick(250);
    await tick(1);
    const option = within(screen.getByRole("group", { name: kind })).getByRole("option");
    expect(option.textContent).toContain("久石譲");
    expect(within(option).queryByText("作曲：久石譲")).toBeNull();
    expect(option.textContent?.split("久石譲")).toHaveLength(2);
  });

  it("超过 200 个 UTF-8 字节不查询或提交，正好 200 字节（trim 后）可提交", async () => {
    const { input, requests, history } = await setup();
    const valid = `${"爱".repeat(66)}ab`;
    change(input, `${valid}c`);
    await tick(1000);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(requests()).toHaveLength(0);
    expect(history.location.pathname).toBe("/");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const errorId = input.getAttribute("aria-describedby");
    expect(errorId === null ? "" : document.getElementById(errorId)?.textContent).toContain("搜索词太长了");
    change(input, `  ${valid}  `);
    expect(input.getAttribute("aria-invalid")).toBeNull();
    await tick(250);
    expect(requests()).toHaveLength(1);
    expect(new URL(requests()[0]?.path ?? "", "http://localhost").searchParams.get("q")).toBe(valid);
    vi.useRealTimers();
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(history.location.pathname).toBe("/search"));
    expect(new URLSearchParams(history.location.search).get("q")).toBe(valid);
  });

  it("空白不查询不提交，清空输入后关闭建议", async () => {
    const { input, requests, history } = await setup();
    change(input, " \t ");
    await tick(500);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(requests()).toHaveLength(0);
    expect(history.location.pathname).toBe("/");
    change(input, "爱");
    await tick(250);
    await tick(1);
    expect(screen.getByRole("listbox")).toBeTruthy();
    change(input, "");
    await tick(500);
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(requests()).toHaveLength(1);
  });

  it("没有结果有明确空态，不把加载状态当成空结果", async () => {
    const { input, api } = await setup();
    const response = deferred<Response>();
    api.on("GET", suggestionsPath, () => response.promise);
    change(input, "没有匹配");
    await tick(250);
    const popup = screen.getByRole("listbox");
    expect(popup.getAttribute("aria-busy")).toBe("true");
    expect(popup.textContent).toContain("正在查找");
    expect(popup.textContent).not.toContain("没有找到");
    await act(async () => response.resolve(sampleResponse("search/suggestions-empty")));
    await tick(1);
    expect(popup.textContent).toContain("没有找到匹配的专辑或曲目");
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(popup.getAttribute("aria-busy")).toBeNull();
  });

  it("建议请求错误会说明失败，仍可按 Enter 查看全部结果", async () => {
    const { input, api, history, runtime } = await setup();
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    api.on("GET", suggestionsPath, () => sampleResponse("errors/database-unavailable"));
    change(input, "爱");
    await tick(250);
    await tick(1);
    expect(screen.getByRole("listbox").textContent).toContain("暂时无法给出建议");
    expect(screen.getByRole("option").textContent).toContain("全部结果");
    vi.useRealTimers();
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(history.location.pathname).toBe("/search"));
  });

  it("旧请求即使迟到也不能覆盖新词的建议", async () => {
    const { input, api, requests } = await setup();
    const old = deferred<Response>();
    api.on("GET", suggestionsPath, (request) => {
      const q = new URL(request.path, "http://localhost").searchParams.get("q");
      return q === "旧词" ? old.promise : jsonResponse(200, { ...sample, query: "爱" });
    });
    change(input, "旧词");
    await tick(250);
    const oldRequest = requests()[0];
    change(input, "爱");
    await tick(250);
    await tick(1);
    expect(oldRequest?.signal?.aborted).toBe(true);
    expect(screen.getAllByRole("option").some((option) => option.textContent?.includes("Summer"))).toBe(true);
    await act(async () => old.resolve(sampleResponse("search/suggestions-empty")));
    await tick(1);
    expect(screen.getByRole("listbox").textContent).not.toContain("没有找到");
    expect(screen.getAllByRole("option").some((option) => option.textContent?.includes("Summer"))).toBe(true);
    expect(screen.getByRole("option", { name: /查看.*全部结果/ }).textContent).toContain("爱");
  });

  it("读屏只在结果稳定后播报一次，输入与方向键移动不会重复播报", async () => {
    const { input, api } = await setup();
    const response = deferred<Response>();
    api.on("GET", suggestionsPath, () => response.promise);
    const box = input.closest("form")?.parentElement;
    if (box === null || box === undefined) throw new Error("搜索框未挂载");
    const status = within(box).getByRole("status");
    const announcements: string[] = [];
    const observer = new MutationObserver(() => {
      if (status.textContent !== "") announcements.push(status.textContent ?? "");
    });
    observer.observe(status, { childList: true, characterData: true, subtree: true });
    try {
      change(input, "a");
      await tick(200);
      change(input, "爱");
      await tick(250);
      expect(status.textContent).toBe("");
      expect(announcements).toEqual([]);
      await act(async () => response.resolve(sampleResponse("search/suggestions")));
      await tick(1);
      expect(status.textContent).toBe("3 条建议");
      fireEvent.keyDown(input, { key: "ArrowDown" });
      fireEvent.keyDown(input, { key: "ArrowDown" });
      fireEvent.keyDown(input, { key: "ArrowUp" });
      await tick(500);
      expect(announcements).toEqual(["3 条建议"]);
    } finally {
      observer.disconnect();
    }
  });
});
