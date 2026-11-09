import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { loadApiSample, sampleBody } from "../../../test/api-samples";
import { deferred } from "../../../test/deferred";
import { fakeZoneServer, testPlayerController, type FakeZoneServer } from "../../../test/fake-zones";
import { jsonResponse, sampleResponse, stubFetch } from "../../../test/http";
import { withValue } from "../../../test/mutate";
import { PlaybackSettingsForm } from "../components/playback-settings-form";
import type { PlayerController, PlayerSnapshot } from "../zones/controller";
import * as api from "./api";
import { IrFilesDialog } from "./ir-files-dialog";

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
let server: FakeZoneServer;
let client: QueryClient;

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  server = fakeZoneServer();
  engine = testPlayerController(server).controller;
  engine.attach();
});

afterEach(() => {
  engine.endSession();
  client.clear();
  vi.restoreAllMocks();
});

describe("卷积最小交互", () => {
  it("上传成功后重读列表并显示新的 IR 文件", async () => {
    const user = userEvent.setup();
    const sample = loadApiSample("dsp/responses/ir-upload");
    const uploaded = api.decodeIrFile(sample.body);
    const completion = deferred<Awaited<ReturnType<typeof api.uploadIrFile>>>();
    const upload = vi.spyOn(api, "uploadIrFile").mockReturnValue(completion.promise);
    let saved = false;
    const requests = stubFetch(({ path, method }) => {
      if (path !== "/api/v1/me/dsp/ir-files" || method !== "GET") throw new Error(`未预期的请求：${method} ${path}`);
      return saved
        ? sampleResponse("dsp/responses/ir-list")
        : jsonResponse(200, withValue(sampleBody("dsp/responses/ir-list"), ["items"], []));
    });
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    render(<QueryClientProvider client={client}><IrFilesDialog open onClose={() => undefined} /></QueryClientProvider>);
    const dialog = screen.getByRole("dialog", { name: "IR 文件" });
    expect(await within(dialog).findByText("还没有 IR 文件")).toBeTruthy();
    expect(within(dialog).queryByText(uploaded.name)).toBeNull();
    const picker = dialog.querySelector('input[type="file"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error("IR 文件选择器不存在");
    const file = new File(["合成上传内容"], uploaded.fileName, { type: "audio/wav" });
    await user.upload(picker, file);
    expect(upload).toHaveBeenCalledTimes(1);
    expect(upload).toHaveBeenCalledWith("impulse", file, {
      signal: expect.any(AbortSignal), onProgress: expect.any(Function),
    });
    expect(within(dialog).getByRole("progressbar", { name: "上传进度" })).toBeTruthy();
    expect(within(dialog).queryByText(uploaded.name)).toBeNull();

    await act(async () => {
      saved = true;
      completion.resolve({ status: sample.status, data: uploaded });
      await completion.promise;
    });
    const name = await within(dialog).findByText(uploaded.name);
    const row = name.closest("li");
    if (row === null) throw new Error("上传成功的 IR 没有列表项");
    expect(within(row).getByText("48 kHz · 1 声道 · 4 抽头")).toBeTruthy();
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(1);
    await waitFor(() => expect(within(dialog).queryByRole("progressbar")).toBeNull());
    expect(within(dialog).queryByText("还没有 IR 文件")).toBeNull();
    expect(requests).toHaveLength(2);
  });

  it("选择 IR 提交完整启用设置，卷积上移后翻转 before_peq", async () => {
    const user = userEvent.setup();
    const file = api.decodeIrFiles(sampleBody("dsp/responses/ir-list"))[0];
    if (file === undefined) throw new Error("IR 列表样本没有文件");
    stubFetch(({ path, method }) => {
      if (path !== "/api/v1/me/dsp/ir-files" || method !== "GET") throw new Error(`未预期的请求：${method} ${path}`);
      return sampleResponse("dsp/responses/ir-list");
    });
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    render(<QueryClientProvider client={client}><PlaybackSettingsForm /></QueryClientProvider>);
    await user.click(screen.getByRole("switch", { name: "参数均衡" }));
    await waitFor(() => expect(engine.getSnapshot().settings.peq.enabled).toBe(true));
    server.commands.length = 0;
    await screen.findByRole("option", { name: file.name });
    await user.selectOptions(screen.getByRole("combobox", { name: "IR 文件" }), file.irId);
    const selected = { enabled: true, ir_id: file.irId, before_peq: false };
    await waitFor(() => expect(server.commands).toEqual([
      { command: "set_settings", settings: { convolution: selected } },
    ]));
    const enabled = screen.getByRole("switch", { name: "卷积" });
    if (!(enabled instanceof HTMLInputElement)) throw new Error("卷积开关不是输入控件");
    await waitFor(() => expect(enabled.checked).toBe(true));
    expect(screen.getByRole("combobox", { name: "IR 文件" }).getAttribute("disabled")).toBeNull();
    await user.click(await screen.findByRole("button", { name: "卷积上移" }));
    await waitFor(() => expect(server.commands).toEqual([
      { command: "set_settings", settings: { convolution: selected } },
      { command: "set_settings", settings: { convolution: { ...selected, before_peq: true } } },
    ]));
    expect(await screen.findByRole("button", { name: "参数均衡上移" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "卷积上移" })).toBeNull();
    expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual(["卷积", "参数均衡"]);
  });
});
