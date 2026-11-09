import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Profiler, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider, type SessionController } from "../../auth";
import { setSessionBoundary } from "../../../shared/api";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import type { FakeAudio } from "../../../test/fake-audio";
import { fakeZoneServer, testPlayerController, type FakeZoneServer } from "../../../test/fake-zones";
import { jsonResponse, sampleResponse, type RecordedRequest } from "../../../test/http";
import { playerTrack } from "../../../test/player-data";
import type { PlayerController } from "../zones/controller";
import { PlayerProvider } from "../player-provider";
import { LyricsPanel } from "./lyrics-panel";

const trackId = "00000000-0000-4000-8000-000000000001";
const controllers: SessionController[] = [];
const players: PlayerController[] = [];

afterEach(() => {
  for (const player of players.splice(0)) player.endSession();
  for (const controller of controllers.splice(0)) controller.dispose();
  setSessionBoundary(null);
  vi.restoreAllMocks();
});

type Mounted = { audio: FakeAudio; server: FakeZoneServer; player: PlayerController; playing: () => FakeAudio | undefined; commits: () => number; api: ReturnType<typeof installFakeApi> };

/** 真实的会话、查询、播放器 Provider 与控制器（播放区服务端与媒体元素是替身），歌词接口按合同回放样本。 */
async function mount(options: { lyrics?: (request: RecordedRequest) => Response | Promise<Response> } = {}): Promise<Mounted> {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  api.on("GET", `/api/v1/tracks/${trackId}/lyrics`, options.lyrics ?? (() => sampleResponse("tracks/lyrics-synced")));
  const controller = createSessionController({ channel: null });
  controllers.push(controller);
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("应已登录");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const server = fakeZoneServer();
  const { controller: player, playing } = testPlayerController(server, { audio: true });
  players.push(player);
  let commits = 0;
  const Wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>
    <SessionProvider controller={controller}>
      <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
        <PlayerProvider userId={state.viewer.id} controller={player}><Profiler id="lyrics" onRender={() => { commits += 1; }}>{children}</Profiler></PlayerProvider>
      </ViewerScopeProvider>
    </SessionProvider>
  </QueryClientProvider>;
  render(<Wrapper><LyricsPanel trackId={trackId} /></Wrapper>);
  const mounted: Mounted = { audio: undefined as unknown as FakeAudio, server, player, playing, api, commits: () => commits };
  return mounted;
}

/** 正在放当前流的元素开始出声。 */
async function sounding(mounted: Mounted): Promise<FakeAudio> {
  await waitFor(() => expect(mounted.playing()?.sources.at(-1)).toBe(mounted.server.zone().stream?.url));
  const audio = mounted.playing();
  if (audio === undefined) throw new Error("没有在放的元素");
  act(() => audio.beginPlaying());
  mounted.audio = audio;
  return audio;
}

/** 曲目开始出声；seekable 为假时服务端说这首不能定位。 */
async function startPlayback(mounted: Mounted, options: { seekable?: boolean } = {}) {
  if (options.seekable === false) mounted.server.afterNext({ position: { seconds: 0, at: Date.now(), durationSeconds: 100, seekable: false } });
  act(() => { mounted.player.playTracks([playerTrack("1", { trackId, durationSeconds: 100 })]); });
  await sounding(mounted);
}

/** 点了一行之后：服务端收到定位，从那里开新流，新流出声。 */
async function seekedTo(mounted: Mounted, seconds: number): Promise<FakeAudio> {
  await waitFor(() => expect(mounted.server.commands.at(-1)).toEqual({ command: "seek", position_seconds: seconds }));
  return sounding(mounted);
}

const lyricsList = () => screen.findByRole("list");
const groups = () => within(screen.getByRole("list")).getAllByRole("listitem");
const current = () => groups().filter((item) => item.getAttribute("aria-current") === "true");
const states = () => groups().map((item) => item.dataset.state);

describe("歌词面板", () => {
  it("加载中如实说明，随后显示同步歌词；面板有“歌词”标题且不设 aria-live", async () => {
    const { api } = await mount();
    expect(screen.getByText("正在加载歌词…")).toBeTruthy();
    await lyricsList();
    expect(screen.getByRole("region", { name: "歌词" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "歌词" })).toBeTruthy();
    expect(api.requestsTo("GET", `/api/v1/tracks/${trackId}/lyrics`)).toHaveLength(1);
    expect(document.querySelector("[aria-live]")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    // 间奏（空文本）不显示文字，也不暴露给读屏；其余每个时间点一组。
    expect(groups().map((item) => item.textContent)).toEqual(["First line of the sample", "Second line of the sample样本的第二行", "Chorus line", "Chorus line"]);
    expect(document.querySelectorAll("li[aria-hidden='true']")).toHaveLength(1);
  });

  it("同步歌词：只有当前组带 aria-current，已过的组、未到的组、间奏各有状态，字号不变", async () => {
    const mounted = await mount();
    await lyricsList();
    await startPlayback(mounted);
    // 位置 0 落在间奏上：没有任何一行是当前行。
    expect(current()).toHaveLength(0);
    expect(states()).toEqual(["upcoming", "upcoming", "upcoming", "upcoming"]);
    act(() => mounted.audio.tick(13));
    expect(current().map((item) => item.textContent)).toEqual(["First line of the sample"]);
    expect(states()).toEqual(["active", "upcoming", "upcoming", "upcoming"]);
    // 同一时间戳的两行是一组，一起成为当前组。
    act(() => mounted.audio.tick(16));
    expect(current().map((item) => item.textContent)).toEqual(["Second line of the sample样本的第二行"]);
    expect(states()).toEqual(["past", "active", "upcoming", "upcoming"]);
    act(() => mounted.audio.tick(99));
    expect(states()).toEqual(["past", "past", "past", "active"]);
    // 回到第一句之前（间奏）：高亮消失。
    act(() => mounted.audio.tick(5));
    expect(current()).toHaveLength(0);
  });

  it("组内约每秒 4 次的进度事件不重绘歌词，当前组变化才重绘", async () => {
    const mounted = await mount();
    await lyricsList();
    await startPlayback(mounted);
    act(() => mounted.audio.tick(13));
    const before = mounted.commits();
    act(() => { mounted.audio.tick(13.25); mounted.audio.tick(13.5); mounted.audio.tick(13.75); mounted.audio.tick(14.9); });
    expect(mounted.commits()).toBe(before);
    act(() => mounted.audio.tick(15.1));
    expect(mounted.commits()).toBeGreaterThan(before);
  });

  it("媒体可拖动时每组是按钮，点击跳到该组时间；不可拖动时是普通文本且提示不再说可点击", async () => {
    const mounted = await mount();
    await lyricsList();
    await startPlayback(mounted, { seekable: false });
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.getByRole("group", { name: "逐行歌词" }).getAttribute("tabindex")).toBe("0");

    act(() => { mounted.player.playTracks([playerTrack("1", { trackId, durationSeconds: 100 })]); });
    await sounding(mounted);
    await waitFor(() => expect(screen.getByRole("group", { name: "逐行歌词" }).hasAttribute("tabindex")).toBe(false));
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((button) => button.textContent)).toEqual(["First line of the sample", "Second line of the sample样本的第二行", "Chorus line", "Chorus line"]);
    await userEvent.click(buttons[2] as HTMLElement);
    const next = await seekedTo(mounted, 20.5);
    // 新流报告位置之后，当前组随之变化；位置比该行早几毫秒也算轮到它，点击后高亮不会停在上一句。
    act(() => next.tick(-0.02));
    expect(current().map((item) => item.textContent)).toEqual(["Chorus line"]);
    expect(states()).toEqual(["past", "past", "active", "upcoming"]);
  });

  it("键盘：Tab 只停在当前组的按钮上，方向键、Home、End 在各句之间移动，Enter 跳转", async () => {
    const mounted = await mount();
    await lyricsList();
    await startPlayback(mounted);
    act(() => mounted.audio.tick(16));
    const buttons = screen.getAllByRole("button");
    expect(buttons.filter((button) => button.tabIndex === 0).map((button) => button.textContent)).toEqual(["Second line of the sample样本的第二行"]);
    await userEvent.tab();
    expect(document.activeElement).toBe(buttons[1]);
    await userEvent.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(buttons[2]);
    await userEvent.keyboard("{End}");
    expect(document.activeElement).toBe(buttons[3]);
    await userEvent.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(buttons[3]);
    await userEvent.keyboard("{Home}");
    expect(document.activeElement).toBe(buttons[0]);
    await userEvent.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(buttons[0]);
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(mounted.server.commands.at(-1)).toEqual({ command: "seek", position_seconds: 12.34 }));
    // 当前组还没有时（位置在第一句之前），第一句是 Tab 停靠点。
    act(() => mounted.audio.tick(1));
    expect(screen.getAllByRole("button").filter((button) => button.tabIndex === 0).map((button) => button.textContent)).toEqual(["First line of the sample"]);
  });

  describe("跟随与暂停跟随", () => {
    const stubLayout = () => {
      // 组 i 的 offsetTop 是 i × 50、高 40；可视区高 400：把第 i 组居中需要 i×50 − 180，下限为 0。
      const index = (element: HTMLElement) => Array.from(element.parentElement?.children ?? []).indexOf(element);
      vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(function offsetTop(this: HTMLElement) { return index(this) * 50; });
      vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(40);
      vi.spyOn(Element.prototype, "clientHeight", "get").mockReturnValue(400);
    };

    it("当前组变化时滚到可视区中部：第一次直接到位，之后平滑；同一组内不再滚动", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted);
      expect(scrollTo).not.toHaveBeenCalled();
      // 在 <ol> 里：间奏是第 0 项，“First line”是第 1 项（50 − 180 < 0 取 0）。
      act(() => mounted.audio.tick(13));
      expect(scrollTo).toHaveBeenCalledTimes(1);
      expect(scrollTo).toHaveBeenLastCalledWith({ top: 0, behavior: "auto" });
      act(() => { mounted.audio.tick(16); });
      expect(scrollTo).toHaveBeenCalledTimes(2);
      expect(scrollTo).toHaveBeenLastCalledWith({ top: 0, behavior: "smooth" });
      act(() => { mounted.audio.tick(16.5); mounted.audio.tick(17); });
      expect(scrollTo).toHaveBeenCalledTimes(2);
      act(() => mounted.audio.tick(45));
      // 最后一组是第 4 项：4 × 50 − (400 − 40) / 2 = 20。
      expect(scrollTo).toHaveBeenLastCalledWith({ top: 20, behavior: "smooth" });
    });

    it("尊重减少动态效果：不平滑滚动", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      vi.spyOn(window, "matchMedia").mockImplementation((query) => ({ matches: query === "(prefers-reduced-motion: reduce)", media: query }) as MediaQueryList);
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted);
      act(() => mounted.audio.tick(13));
      act(() => mounted.audio.tick(45));
      expect(scrollTo.mock.calls.map(([options]) => (options as ScrollToOptions).behavior)).toEqual(["auto", "auto"]);
    });

    it("当前组是间奏时停在上一句，不跳到空白行；下一句开始时再滚动", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      const lines = [{ start_ms: 1000, text: "a" }, { start_ms: 5000, text: "" }, { start_ms: 9000, text: "b" }];
      const mounted = await mount({ lyrics: () => jsonResponse(200, { track_id: trackId, lyrics: { kind: "synced", source: "embedded", lines } }) });
      await lyricsList();
      await startPlayback(mounted);
      act(() => mounted.audio.tick(2));
      expect(current().map((item) => item.textContent)).toEqual(["a"]);
      expect(scrollTo).toHaveBeenCalledTimes(1);
      // 间奏开始：上一句不再高亮，但视图停在那里，不滚到空白行。
      act(() => mounted.audio.tick(6));
      expect(current()).toHaveLength(0);
      expect(scrollTo).toHaveBeenCalledTimes(1);
      act(() => mounted.audio.tick(10));
      expect(current().map((item) => item.textContent)).toEqual(["b"]);
      expect(scrollTo).toHaveBeenCalledTimes(2);
    });

    it("用户的滚轮、触摸与滚动键暂停跟随并出现“回到当前行”；程序触发的滚动与 Tab、Enter 不算", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted);
      act(() => mounted.audio.tick(13));
      const region = screen.getByRole("group", { name: "逐行歌词" });
      const back = () => screen.queryByRole("button", { name: "回到当前行" });

      fireEvent.scroll(region);
      fireEvent.keyDown(region, { key: "Tab" });
      fireEvent.keyDown(region, { key: "Enter" });
      // 空格落在行按钮上是“跳到这一行”，不是滚动。
      fireEvent.keyDown(screen.getAllByRole("button")[0] ?? region, { key: " " });
      expect(back()).toBeNull();

      for (const [name, act1] of [
        ["滚轮", () => fireEvent.wheel(region)],
        ["触摸", () => fireEvent.touchMove(region)],
        ["PageDown", () => fireEvent.keyDown(region, { key: "PageDown" })],
        ["向上方向键", () => fireEvent.keyDown(region, { key: "ArrowUp" })],
        ["拖动滚动条", () => fireEvent.pointerDown(region)],
      ] as const) {
        const beforePause = scrollTo.mock.calls.length;
        act(() => act1());
        expect(back(), name).not.toBeNull();
        // 暂停期间当前组变化不再滚动。
        act(() => mounted.audio.tick(16));
        expect(scrollTo.mock.calls.length, name).toBe(beforePause);
        // 点“回到当前行”：恢复跟随并立即定位。
        await userEvent.click(back() as HTMLElement);
        expect(back(), name).toBeNull();
        expect(scrollTo.mock.calls.length, name).toBe(beforePause + 1);
        act(() => mounted.audio.tick(13));
        scrollTo.mockClear();
      }
    });

    it("媒体不可拖动时可聚焦的歌词区域本身收到空格键就是在滚动，同样暂停跟随", async () => {
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted, { seekable: false });
      act(() => mounted.audio.tick(13));
      const region = screen.getByRole("group", { name: "逐行歌词" });
      expect(screen.queryByRole("button", { name: "回到当前行" })).toBeNull();
      fireEvent.keyDown(region, { key: " " });
      expect(screen.getByRole("button", { name: "回到当前行" })).toBeTruthy();
    });

    it("暂停跟随后点某一行：跳转并恢复跟随", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted);
      act(() => mounted.audio.tick(13));
      fireEvent.wheel(screen.getByRole("group", { name: "逐行歌词" }));
      expect(screen.getByRole("button", { name: "回到当前行" })).toBeTruthy();
      const calls = scrollTo.mock.calls.length;
      const chorus = screen.getAllByRole("button", { name: "Chorus line" })[0];
      if (chorus === undefined) throw new Error("应有“Chorus line”按钮");
      await userEvent.click(chorus);
      expect(screen.queryByRole("button", { name: "回到当前行" })).toBeNull();
      const next = await seekedTo(mounted, 20.5);
      act(() => next.tick(0.5));
      expect(scrollTo.mock.calls.length).toBeGreaterThan(calls);
    });

    it("键盘可完成“回到当前行”：Tab 从歌词里唯一的停靠点（当前行）走到按钮，Enter 恢复跟随并定位", async () => {
      stubLayout();
      const scrollTo = vi.spyOn(Element.prototype, "scrollTo").mockImplementation(() => undefined);
      const mounted = await mount();
      await lyricsList();
      await startPlayback(mounted);
      act(() => mounted.audio.tick(16));
      fireEvent.keyDown(screen.getByRole("group", { name: "逐行歌词" }), { key: "PageDown" });
      const back = screen.getByRole("button", { name: "回到当前行" });
      const calls = scrollTo.mock.calls.length;
      await userEvent.tab();
      expect(document.activeElement?.textContent).toBe("Second line of the sample样本的第二行");
      await userEvent.tab();
      expect(document.activeElement).toBe(back);
      await userEvent.keyboard("{Enter}");
      expect(screen.queryByRole("button", { name: "回到当前行" })).toBeNull();
      expect(scrollTo.mock.calls.length).toBe(calls + 1);
    });
  });

  it("纯文本歌词：空行分段、静态显示，不高亮、不可点，可聚焦滚动", async () => {
    const mounted = await mount({ lyrics: () => sampleResponse("tracks/lyrics-plain") });
    await screen.findByRole("group", { name: "歌词文本" });
    await startPlayback(mounted);
    const paragraphs = Array.from(document.querySelectorAll("p")).filter((paragraph) => paragraph.querySelector("span"));
    expect(paragraphs.map((paragraph) => Array.from(paragraph.children).map((line) => line.textContent))).toEqual([
      ["First verse, line one", "First verse, line two"],
      ["Second verse, line one"],
    ]);
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(document.querySelector("[aria-current]")).toBeNull();
    expect(screen.getByRole("group", { name: "歌词文本" }).getAttribute("tabindex")).toBe("0");
    act(() => mounted.audio.tick(30));
    expect(document.querySelector("[aria-current]")).toBeNull();
  });

  it("没有歌词是空态说明，不是错误，也不生成占位歌词", async () => {
    await mount({ lyrics: () => sampleResponse("tracks/lyrics-none") });
    expect(await screen.findByText("这首曲目没有内嵌歌词")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.getByRole("heading", { level: 2, name: "歌词" })).toBeTruthy();
  });

  it("读取失败显示带请求 ID 的错误与重试，重试成功后显示歌词", async () => {
    const mounted = await mount({ lyrics: () => sampleResponse("errors/database-unavailable") });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("服务暂时不可用");
    expect(screen.queryByText("这首曲目没有内嵌歌词")).toBeNull();
    mounted.api.on("GET", `/api/v1/tracks/${trackId}/lyrics`, () => sampleResponse("tracks/lyrics-synced"));
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await lyricsList();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(mounted.api.requestsTo("GET", `/api/v1/tracks/${trackId}/lyrics`)).toHaveLength(2);
  });

  it("曲目不存在（404）按错误显示，不当成“没有歌词”", async () => {
    await mount({ lyrics: () => sampleResponse("errors/not-found-track") });
    expect((await screen.findByRole("alert")).textContent).toContain("不存在");
    expect(screen.queryByText("这首曲目没有内嵌歌词")).toBeNull();
  });
});
