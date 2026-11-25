import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { decode as d } from "../shared/api";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const statusPath = "/api/v1/admin/insights";
const ackPath = "/api/v1/admin/insights/notice-acknowledgement";
const aboutPath = "/api/v1/system/about";
const versionPath = "/api/v1/system/version";
const diagnosticsPath = "/api/v1/admin/diagnostics";
const repo = "https://github.com/qlqqs/ROOMusic";
const version = "0.8.1";
const commit = "b6654580039af4cd484397f8d5d42de14eca3641";
const enabledBody = d.defineDecoder(d.object({ enabled: d.boolean() }));
type Status = Record<string, unknown> & { enabled: boolean; forced_off: boolean; notice_acknowledged: boolean };

function setup({ current = "curator", status = {} }: { current?: "curator" | "listener"; status?: Partial<Status> } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current });
  let state = { ...(sampleBody("insights/status") as Status), ...status };
  api.on("GET", statusPath, () => jsonResponse(200, state));
  api.on("PUT", statusPath, ({ body }) => {
    if (state.forced_off) return jsonResponse(409, { error: { code: "insights_forced_off", message: "部署配置已关闭匿名统计" }, request_id: "r" });
    state = { ...state, enabled: enabledBody(body).enabled };
    return jsonResponse(200, state);
  });
  api.on("POST", ackPath, () => {
    state = { ...state, notice_acknowledged: true };
    return new Response(null, { status: 204 });
  });
  api.on("GET", aboutPath, () => jsonResponse(200, sampleBody("insights/about")));
  return api;
}

function renderAbout(current: "curator" | "listener" = "curator") {
  const api = setup({ current });
  api.on("GET", versionPath, () => jsonResponse(200, { version, commit }));
  renderApp({ path: "/settings/about" });
  return api;
}

describe("匿名统计", () => {
  it("概览说明：管理员“知道了”后记为已确认、不再显示", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/" });
    expect(await screen.findByText(/每天向项目发送一次匿名统计/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "查看发送内容" }).getAttribute("href")).toBe("/settings/insights");
    await user.click(screen.getByRole("button", { name: "知道了" }));
    await waitFor(() => expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull());
    expect(api.requestsTo("POST", ackPath)).toHaveLength(1);
    expect(api.requestsTo("PUT", statusPath)).toHaveLength(0);
  });

  it("概览说明：“关闭统计”先关开关再确认", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/" });
    await user.click(await screen.findByRole("button", { name: "关闭统计" }));
    await waitFor(() => expect(api.requestsTo("POST", ackPath)).toHaveLength(1));
    expect(api.requestsTo("PUT", statusPath).map(({ body }) => body)).toEqual([{ enabled: false }]);
    await waitFor(() => expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull());
  });

  it("已确认后不显示说明", async () => {
    setup({ status: { notice_acknowledged: true } });
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull();
  });

  it("成员看不到说明，也不请求管理接口", async () => {
    const api = setup({ current: "listener" });
    renderApp({ path: "/" });
    expect(await screen.findByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(screen.queryByText(/每天向项目发送一次匿名统计/)).toBeNull();
    expect(api.requestsTo("GET", statusPath)).toHaveLength(0);
  });

  it("设置页：开关、上次发送与完整载荷", async () => {
    const user = userEvent.setup();
    const api = setup();
    renderApp({ path: "/settings/insights" });
    const toggle = await screen.findByRole<HTMLInputElement>("switch", { name: "发送匿名统计" });
    expect(toggle.checked).toBe(true);
    expect(screen.getByText(/还没有发送过；最早在/)).toBeTruthy();
    const payload = screen.getByLabelText("将要发送的内容");
    expect(payload.textContent).toContain('"instance_id": "00000000-0000-4000-8000-000000000001"');
    expect(payload.textContent).toContain('"tracks": "0"');
    await user.click(toggle);
    await waitFor(() => expect(toggle.checked).toBe(false));
    expect(api.requestsTo("PUT", statusPath).map(({ body }) => body)).toEqual([{ enabled: false }]);
  });

  it("部署层关闭时开关不可改，说明原因", async () => {
    setup({ status: { forced_off: true, enabled: true } });
    renderApp({ path: "/settings/insights" });
    const toggle = await screen.findByRole<HTMLInputElement>("switch", { name: "发送匿名统计" });
    expect(toggle.disabled).toBe(true);
    expect(toggle.checked).toBe(false);
    expect(screen.getByText(/ROOMUSIC_INSIGHTS_DISABLED/)).toBeTruthy();
  });
});

describe("设置 › 关于", () => {
  afterEach(() => vi.restoreAllMocks());

  it("成员看到项目与反馈链接，版本和诊断信息仅供管理员查看", async () => {
    const api = renderAbout("listener");
    const project = within(await screen.findByRole("region", { name: "项目" }));
    const feedback = within(screen.getByRole("region", { name: "反馈" }));
    const links = [
      ["源代码", repo],
      ["更新说明", `${repo}/releases`],
      ["许可 AGPL-3.0", "https://www.gnu.org/licenses/agpl-3.0.html"],
      ["第三方许可", `${repo}/blob/main/THIRD-PARTY-NOTICES.md`],
    ] as const;
    for (const [name, href] of links) {
      expect(project.getByRole("link", { name }).getAttribute("href")).toBe(href);
    }
    expect(feedback.getByRole("link", { name: "反馈问题" }).getAttribute("href")).toBe(`${repo}/issues/new?template=bug_report.yml`);
    const main = within(screen.getByRole("main"));
    expect(main.queryByText("版本")).toBeNull();
    expect(main.queryByText(version)).toBeNull();
    expect(main.queryByRole("link", { name: "b665458" })).toBeNull();
    expect(main.queryByRole("button", { name: /诊断信息/ })).toBeNull();
    expect(api.requestsTo("GET", versionPath)).toHaveLength(0);
    expect(api.requestsTo("GET", diagnosticsPath)).toHaveLength(0);
  });

  it("管理员看到版本、commit 和带版本的反馈链接", async () => {
    renderAbout();
    const about = within(await screen.findByRole("region", { name: "ROOMusic" }));
    expect(await about.findByText(version)).toBeTruthy();
    expect(about.getByRole("link", { name: "b665458" }).getAttribute("href")).toBe(`${repo}/commit/${commit}`);
    const project = within(screen.getByRole("region", { name: "项目" }));
    const feedback = within(screen.getByRole("region", { name: "反馈" }));
    expect(project.getByRole("link", { name: `${version} 的更新说明` }).getAttribute("href")).toBe(`${repo}/releases/tag/v${version}`);
    expect(feedback.getByRole("link", { name: "反馈问题" }).getAttribute("href")).toBe(`${repo}/issues/new?template=bug_report.yml&version=0.8.1+%28b665458%29`);
  });

  it("管理员点击后复制带 JSON 围栏的诊断报告并更新按钮读屏名", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("ClipboardItem", undefined);
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const api = renderAbout();
    await user.click(await screen.findByRole("button", { name: "复制诊断信息" }));
    expect(await screen.findByRole("button", { name: "已复制诊断信息" })).toBeTruthy();
    expect(writeText).toHaveBeenCalledExactlyOnceWith(`\`\`\`json\n${JSON.stringify(sampleBody("diagnostics/admin"), null, 2)}\n\`\`\`\n`);
    expect(api.requestsTo("GET", diagnosticsPath)).toHaveLength(1);
  });

  it("诊断接口返回 503 时显示错误提示", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("ClipboardItem", undefined);
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const api = renderAbout();
    api.on("GET", diagnosticsPath, () => jsonResponse(503, {
      error: { code: "diagnostics_unavailable", message: "暂时无法读取诊断信息" },
      request_id: "sample-request",
    }));
    await user.click(await screen.findByRole("button", { name: "复制诊断信息" }));
    expect((await screen.findByRole("alert")).textContent).toContain("没有拿到诊断信息");
    expect(writeText).not.toHaveBeenCalled();
  });

  it("ClipboardItem 写入先被拒绝、诊断接口随后返回 503 时显示错误且没有未处理拒绝", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("ClipboardItem", class {
      constructor(readonly items: Record<string, Promise<Blob>>) {}
    });
    const write = vi.spyOn(navigator.clipboard, "write").mockRejectedValue(new DOMException("剪贴板权限被拒绝", "NotAllowedError"));
    const response = deferred<Response>();
    const api = renderAbout();
    api.on("GET", diagnosticsPath, () => response.promise);

    await user.click(await screen.findByRole("button", { name: "复制诊断信息" }));
    await waitFor(() => expect(write).toHaveBeenCalledExactlyOnceWith([expect.any(ClipboardItem)]));
    expect(screen.getByRole("button", { name: "正在复制诊断信息" })).toBeTruthy();

    // 剪贴板替身没有消费 Blob Promise，泄漏的拒绝会由 Vitest 报错。
    response.resolve(jsonResponse(503, {
      error: { code: "diagnostics_unavailable", message: "暂时无法读取诊断信息" },
      request_id: "sample-request",
    }));
    expect((await screen.findByRole("alert")).textContent).toContain("没有拿到诊断信息");
    expect(screen.getByRole("button", { name: "复制诊断信息" })).toBeTruthy();
  });

  it.each([
    { scenario: "非安全上下文", secureContext: false, buttonName: "下载诊断信息", writeAttempts: 0 },
    { scenario: "剪贴板写入被拒绝后", secureContext: true, buttonName: "复制诊断信息", writeAttempts: 1 },
  ])("$scenario 下载 JSON 文件并更新按钮读屏名", async ({ secureContext, buttonName, writeAttempts }) => {
    const user = userEvent.setup();
    vi.stubGlobal("isSecureContext", secureContext);
    vi.stubGlobal("ClipboardItem", undefined);
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new DOMException("剪贴板权限被拒绝", "NotAllowedError"));
    const createObjectURL = vi.fn<(blob: Blob) => string>().mockReturnValue("blob:diagnostics");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", class extends URL {
      static createObjectURL = createObjectURL;
      static revokeObjectURL = revokeObjectURL;
    });
    const downloads: { href: string; filename: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push({ href: this.href, filename: this.download });
    });
    renderAbout();
    await user.click(await screen.findByRole("button", { name: buttonName }));
    expect(await screen.findByRole("button", { name: "已下载诊断信息" })).toBeTruthy();
    expect(downloads).toEqual([{ href: "blob:diagnostics", filename: "roomusic-diagnostics-2026-01-01T00-00-02Z.json" }]);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls[0]?.[0];
    if (blob === undefined) throw new Error("缺少诊断报告文件");
    expect(blob.type).toBe("application/json");
    const text = await new Promise<string | ArrayBuffer | null>((resolve) => {
      const reader = new FileReader();
      reader.addEventListener("loadend", () => resolve(reader.result));
      reader.readAsText(blob);
    });
    expect(text).toBe(JSON.stringify(sampleBody("diagnostics/admin"), null, 2));
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:diagnostics");
    expect(writeText).toHaveBeenCalledTimes(writeAttempts);
  });
});
