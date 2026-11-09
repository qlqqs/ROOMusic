import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { loadApiSample, sampleBody } from "../../../test/api-samples";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import type { PlayerController, PlayerSnapshot } from "../zones/controller";
import { decodeProduct } from "./api";
import { peqToWire, type PeqDocument } from "./document";
import { PeqEditorDialog } from "./peq-editor";

vi.mock("../../auth", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../auth")>(),
  useViewerScope: () => ({ generation: 1 }),
}));
vi.mock("../player-provider", () => ({
  usePlayerEngine: () => engine,
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) {
    return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot()));
  },
}));

let engine: PlayerController;
let client: QueryClient;
const requests: { path: string; method: string | undefined; body: unknown }[] = [];

beforeEach(() => {
  requests.length = 0;
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  engine = testPlayerController(fakeZoneServer()).controller;
  engine.attach();
  vi.stubGlobal("fetch", vi.fn<typeof fetch>(async (input, init) => {
    const path = String(input);
    requests.push({ path, method: init?.method, body: typeof init?.body === "string" ? JSON.parse(init.body) : null });
    const samples: Record<string, string> = {
      "/api/v1/me/dsp/peq/response": "peq-response",
      "/api/v1/me/dsp/peq/import": "peq-import",
      "/api/v1/me/dsp/opra/search?q=HD650": "opra-search",
      "/api/v1/me/dsp/opra/products/sennheiser%3A%3Ahd650": "opra-product",
    };
    const name = samples[path];
    if (name === undefined) throw new Error(`未预期的请求：${path}`);
    const sample = loadApiSample(`dsp/responses/${name}`);
    return new Response(JSON.stringify(sample.body), { status: sample.status, headers: { "Content-Type": "application/json" } });
  }));
});

afterEach(() => {
  engine.endSession();
  client.clear();
  vi.restoreAllMocks();
});

async function viewEditor() {
  await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
  const submit = vi.spyOn(engine, "setPlaybackSettings");
  render(<QueryClientProvider client={client}><PeqEditorDialog open onClose={() => undefined} /></QueryClientProvider>);
  return submit;
}

function submittedPeq(submit: MockInstance<PlayerController["setPlaybackSettings"]>): PeqDocument {
  const peq = submit.mock.calls.at(-1)?.[0].peq;
  if (peq === undefined) throw new Error("没有提交完整 PEQ 文档");
  return peq;
}

describe("PEQ 最小交互", () => {
  it("添加、改类型提交完整文档，数字编辑不提交，失焦或回车只提交一次", async () => {
    const user = userEvent.setup();
    const submit = await viewEditor();
    await user.click(screen.getByRole("button", { name: "添加一段" }));
    expect(submit).toHaveBeenCalledTimes(1);
    expect(peqToWire(submittedPeq(submit))).toEqual({
      enabled: false, preamp_db: 0,
      bands: [{ type: "peaking", freq_hz: 1000, gain_db: 0, q: 0.71, channels: "all", enabled: true }],
    });

    await user.selectOptions(screen.getByRole("combobox", { name: "第 1 段类型" }), "low_pass");
    await user.selectOptions(screen.getByRole("combobox", { name: "第 1 段斜率" }), "24");
    const lowPass = {
      enabled: false, preamp_db: 0,
      bands: [{ type: "low_pass", freq_hz: 1000, slope_db: 24, channels: "all", enabled: true }],
    };
    expect(submit).toHaveBeenCalledTimes(3);
    expect(peqToWire(submittedPeq(submit))).toEqual(lowPass);
    expect(screen.queryByRole("spinbutton", { name: "第 1 段增益" })).toBeNull();
    expect(screen.queryByRole("spinbutton", { name: "第 1 段 Q" })).toBeNull();
    await waitFor(() => expect(peqToWire(engine.getSnapshot().settings.peq)).toEqual(lowPass));

    submit.mockClear();
    const preamp = screen.getByRole("spinbutton", { name: "前级增益" });
    await user.clear(preamp);
    await user.type(preamp, "-6");
    expect(submit).not.toHaveBeenCalled();
    await user.tab();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(peqToWire(submittedPeq(submit))).toEqual({ ...lowPass, preamp_db: -6 });
    await waitFor(() => expect(engine.getSnapshot().settings.peq.preampDb).toBe(-6));

    submit.mockClear();
    await user.clear(preamp);
    await user.type(preamp, "-9");
    expect(submit).not.toHaveBeenCalled();
    // 服务端响应到达前，回车紧接失焦仍是同一次确认，不能重复发命令。
    act(() => {
      fireEvent.keyDown(preamp, { key: "Enter" });
      fireEvent.blur(preamp);
    });
    expect(submit).toHaveBeenCalledTimes(1);
    expect(peqToWire(submittedPeq(submit))).toEqual({ ...lowPass, preamp_db: -9 });
  });

  it("文本导入先预览，应用后提交解析得到的完整文档", async () => {
    const user = userEvent.setup();
    const submit = await viewEditor();
    await user.click(screen.getByRole("button", { name: "粘贴导入" }));
    const dialog = screen.getByRole("dialog", { name: "粘贴导入" });
    const apply = within(dialog).getByRole("button", { name: "应用" });
    expect(apply.hasAttribute("disabled")).toBe(true);
    const text = "Preamp: -3 dB\nFilter 1: ON PK Fc 1000 Hz Gain 6 dB Q 1\nChannel: L";
    await user.click(within(dialog).getByRole("textbox", { name: "参数" }));
    await user.paste(text);
    expect(await within(dialog).findByText("1 段，有 1 行没有识别")).toBeTruthy();
    const curve = within(dialog).getByRole("img", { name: "频响曲线" });
    await waitFor(() => expect(curve.querySelector("polyline")).not.toBeNull());
    expect(requests).toContainEqual({ path: "/api/v1/me/dsp/peq/import", method: "POST", body: { text } });
    expect(submit).not.toHaveBeenCalled();

    await user.click(apply);
    expect(submit).toHaveBeenCalledTimes(1);
    expect(peqToWire(submittedPeq(submit))).toEqual({
      enabled: true, preamp_db: -3, source: { kind: "import" },
      bands: [{ type: "peaking", freq_hz: 1000, gain_db: 6, q: 1, channels: "all", enabled: true }],
    });
    expect(screen.queryByRole("dialog", { name: "粘贴导入" })).toBeNull();
    expect(within(screen.getByRole("dialog", { name: "参数均衡" })).getByText("导入", { exact: true })).toBeTruthy();
  });

  it("搜索 OPRA 并套用配置后，编辑器头部显示作者和来源链接", async () => {
    const user = userEvent.setup();
    const submit = await viewEditor();
    const selected = decodeProduct(sampleBody("dsp/responses/opra-product")).eqs[0];
    if (selected === undefined) throw new Error("OPRA 样本没有配置");
    await user.click(screen.getByRole("button", { name: "耳机校正" }));
    const dialog = screen.getByRole("dialog", { name: "耳机校正" });
    await user.type(within(dialog).getByRole("searchbox", { name: "耳机型号" }), "HD650");
    await user.click(await within(dialog).findByRole("button", { name: /^Sennheiser HD650/ }));
    const details = await within(dialog).findByText("Harman Target", { exact: true });
    const row = details.closest("li");
    if (row === null) throw new Error("OPRA 配置没有列表项");
    expect(within(row).getByText(selected.author)).toBeTruthy();
    expect(within(row).getByRole("link", { name: "来源" }).getAttribute("href")).toBe(selected.link);
    expect(submit).not.toHaveBeenCalled();
    await user.click(within(row).getByRole("button", { name: "套用" }));

    expect(submit).toHaveBeenCalledTimes(1);
    expect(submittedPeq(submit)).toEqual(selected.peq);
    expect(screen.queryByRole("dialog", { name: "耳机校正" })).toBeNull();
    const editor = screen.getByRole("dialog", { name: "参数均衡" });
    const heading = within(editor).getByRole("heading", { name: "参数均衡" }).parentElement;
    if (heading === null) throw new Error("编辑器没有标题区域");
    expect(heading.textContent).toContain(`HD650 · ${selected.author}`);
    expect(within(heading).getByRole("link", { name: selected.author }).getAttribute("href")).toBe(selected.link);
  });
});
