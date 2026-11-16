import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const tokensPath = "/api/v1/me/steward/tokens";
const tokenPath = /^\/api\/v1\/me\/steward\/tokens\/[^/]+$/;
const secret = "rmt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAbcd";

type Token = { id: string; name: string; scope: "read" | "read_write"; tail: string; created_at: string; expires_at: string | null; last_used_at: string | null };

const existing: Token = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "书房电脑",
  scope: "read",
  tail: "wxyz",
  created_at: "2026-11-01T00:00:00Z",
  expires_at: null,
  last_used_at: null,
};

/** 令牌接口的替身：列表、签发（返回一次明文）、吊销；形状按 C3 的接口设计。 */
function setup(options: { role?: "admin" | "user"; available?: boolean; tokens?: Token[]; createFailure?: Response } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const state = { tokens: options.tokens ?? [existing] };
  api.on("GET", tokensPath, () => jsonResponse(200, { available: options.available ?? true, tokens: state.tokens }));
  api.on("POST", tokensPath, (request) => {
    if (options.createFailure !== undefined) return options.createFailure;
    const input = request.body as { name: string; scope: Token["scope"]; expires_at: string | null };
    const created: Token = { id: "00000000-0000-4000-8000-000000000002", name: input.name, scope: input.scope, tail: "Abcd", created_at: "2026-11-16T00:00:00Z", expires_at: input.expires_at, last_used_at: null };
    state.tokens = [...state.tokens, created];
    return jsonResponse(201, { ...created, token: secret });
  });
  api.on("DELETE", tokenPath, (request) => {
    state.tokens = state.tokens.filter((token) => !request.path.endsWith(token.id));
    return new Response(null, { status: 204 });
  });
  return { api, state };
}

async function openTokens(options: Parameters<typeof setup>[0] = {}) {
  const context = setup(options);
  renderApp({ path: "/settings/tokens" });
  await screen.findByRole("heading", { level: 1, name: "访问令牌" });
  return context;
}

describe("设置 › 访问令牌", () => {
  it("列出令牌：名称、权限、末四位、不过期与从未使用", async () => {
    await openTokens();
    const row = (await screen.findByRole("rowheader", { name: "书房电脑" })).closest("tr");
    expect(row).not.toBeNull();
    const cells = within(row as HTMLElement);
    expect(cells.getByText("只读")).toBeTruthy();
    expect(cells.getByText("wxyz")).toBeTruthy();
    expect(cells.getByText("不过期")).toBeTruthy();
    expect(cells.getByText("从未使用")).toBeTruthy();
  });

  it("成员也能打开这一栏", async () => {
    await openTokens({ role: "user" });
    expect(await screen.findByRole("rowheader", { name: "书房电脑" })).toBeTruthy();
  });

  it("新建令牌：提交名称、权限与有效期，明文与接入命令只在对话框里显示，关闭后页面上不再有明文", async () => {
    const { api } = await openTokens({ tokens: [] });
    expect(await screen.findByText("还没有令牌")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "新建令牌" }));
    const dialog = screen.getByRole("dialog", { name: "新建访问令牌" });
    await userEvent.type(within(dialog).getByLabelText("名称"), "  笔记本  ");
    await userEvent.click(within(dialog).getByRole("radio", { name: "读写" }));
    await userEvent.click(within(dialog).getByRole("radio", { name: "不过期" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "创建" }));

    const done = await screen.findByRole("dialog", { name: "令牌“笔记本”已创建" });
    expect(api.requestsTo("POST", tokensPath).map((request) => request.body)).toEqual([{ name: "笔记本", scope: "read_write", expires_at: null }]);
    expect((within(done).getByLabelText("令牌") as HTMLTextAreaElement).value).toBe(secret);
    expect((within(done).getByLabelText("在 Claude Code 里接入") as HTMLTextAreaElement).value).toBe(
      `claude mcp add --transport http roomusic ${window.location.origin}/api/v1/mcp --header "Authorization: Bearer ${secret}"`,
    );

    await userEvent.click(within(done).getByRole("button", { name: "完成" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByRole("rowheader", { name: "笔记本" })).toBeTruthy();
    expect(document.body.textContent).not.toContain(secret);
  });

  it("有效期按天数换算成过期时间", async () => {
    const { api } = await openTokens({ tokens: [] });
    await userEvent.click(await screen.findByRole("button", { name: "新建令牌" }));
    await userEvent.type(screen.getByLabelText("名称"), "默认");
    const before = Date.now();
    await userEvent.click(screen.getByRole("button", { name: "创建" }));
    await screen.findByRole("dialog", { name: "令牌“默认”已创建" });
    const body = api.requestsTo("POST", tokensPath)[0]?.body as { scope: string; expires_at: string };
    expect(body.scope).toBe("read");
    const days = (Date.parse(body.expires_at) - before) / (24 * 60 * 60 * 1000);
    expect(days).toBeGreaterThanOrEqual(90);
    expect(days).toBeLessThan(90.01);
  });

  it("名称为空时不发请求，错误显示在输入框旁", async () => {
    const { api } = await openTokens({ tokens: [] });
    await userEvent.click(await screen.findByRole("button", { name: "新建令牌" }));
    await userEvent.click(screen.getByRole("button", { name: "创建" }));
    expect(screen.getByText("名称需要 1 到 64 个字。")).toBeTruthy();
    expect(screen.getByLabelText("名称").getAttribute("aria-invalid")).toBe("true");
    expect(api.requestsTo("POST", tokensPath)).toHaveLength(0);
  });

  it("签发被拒绝时错误留在对话框里，保留输入", async () => {
    await openTokens({ tokens: [], createFailure: jsonResponse(409, { error: { code: "token_limit_reached", message: "limit" }, request_id: "req-1" }) });
    await userEvent.click(await screen.findByRole("button", { name: "新建令牌" }));
    await userEvent.type(screen.getByLabelText("名称"), "多一个");
    await userEvent.click(screen.getByRole("button", { name: "创建" }));
    const dialog = screen.getByRole("dialog", { name: "新建访问令牌" });
    expect((await within(dialog).findByRole("alert")).textContent).toContain("令牌数量已到上限，请先吊销不用的令牌。");
    expect((within(dialog).getByLabelText("名称") as HTMLInputElement).value).toBe("多一个");
  });

  it("吊销先确认，确认后发 DELETE，列表里不再有它", async () => {
    const { api } = await openTokens();
    await userEvent.click(await screen.findByRole("button", { name: "吊销令牌 书房电脑" }));
    const dialog = screen.getByRole("dialog", { name: "吊销令牌“书房电脑”？" });
    expect(api.requestsTo("DELETE", tokenPath)).toHaveLength(0);
    await userEvent.click(within(dialog).getByRole("button", { name: "吊销令牌" }));
    expect(await screen.findByText("已吊销令牌“书房电脑”，用它的客户端不能再连接。")).toBeTruthy();
    expect(api.requestsTo("DELETE", tokenPath).map((request) => request.path)).toEqual([`${tokensPath}/${existing.id}`]);
    await waitFor(() => expect(screen.queryByRole("rowheader", { name: "书房电脑" })).toBeNull());
  });

  it("成员开关关闭时说明原因，不能新建，仍能吊销已有的令牌", async () => {
    await openTokens({ role: "user", available: false });
    expect(await screen.findByText("管理员已关闭成员使用管家，已有的令牌暂时不能使用，也不能新建。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "新建令牌" }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: "吊销令牌 书房电脑" }).hasAttribute("disabled")).toBe(false);
  });
});
