import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { newPasswordRuleMessage } from "../features/auth/account-rules";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { FakeAudio } from "../test/fake-audio";

beforeEach(() => { vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } }); });

const passwordPath = "/api/v1/auth/password";
const oldPassword = "curator-password";
const newPassword = "new account password";

async function openPasswordChange() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  const channel = { notifyChanged: vi.fn(), onChanged: () => () => undefined, close: vi.fn() };
  const rendered = renderApp({ path: "/members", channel });
  await screen.findByRole("rowheader", { name: /^listener/ });
  const trigger = screen.getByRole("button", { name: "修改密码" });
  await userEvent.click(trigger);
  const dialogElement = await screen.findByRole("dialog", { name: "修改密码" });
  return { api, session, channel, ...rendered, trigger, dialogElement, dialog: within(dialogElement) };
}

type Context = Awaited<ReturnType<typeof openPasswordChange>>;

async function fill(context: Context, current = oldPassword, password = newPassword, confirmation = password) {
  for (const [label, value] of [["当前密码", current], ["新密码", password], ["确认新密码", confirmation]] as const) {
    const input = context.dialog.getByLabelText<HTMLInputElement>(label);
    await userEvent.clear(input);
    if (value) await userEvent.type(input, value);
  }
}

const submit = (context: Context) => userEvent.click(context.dialog.getByRole("button", { name: "修改密码" }));

function expectFieldError(context: Context, label: string, message: string) {
  const input = context.dialog.getByLabelText(label);
  expect(input.getAttribute("aria-invalid")).toBe("true");
  const descriptions = input.getAttribute("aria-describedby")?.split(" ").map((id) => document.getElementById(id)?.textContent).join(" ");
  expect(descriptions).toContain(message);
  expect(document.activeElement).toBe(input);
}

/** 成功后留在对话框里：结果只读一次，焦点交给唯一的“完成”按钮。 */
async function expectPasswordChanged(context: Context) {
  const done = await context.dialog.findByRole<HTMLButtonElement>("button", { name: "完成" });
  expect(screen.getByRole("dialog", { name: "修改密码" })).toBe(context.dialogElement);
  expect(context.dialog.getByRole("heading", { level: 2, name: "修改密码" })).toBeTruthy();
  const result = context.dialog.getByText("密码已修改。");
  expect(screen.getAllByText("密码已修改。")).toEqual([result]);
  expect(result.closest("dialog")).toBe(context.dialogElement);
  expect(context.dialog.queryByRole("status")).toBeNull();
  expect(context.dialog.queryByRole("alert")).toBeNull();
  expect(result.closest('[role="status"], [aria-live]')).toBeNull();
  expect(context.dialog.getAllByRole("button")).toEqual([done]);
  expect(done.disabled).toBe(false);
  const describedBy = done.getAttribute("aria-describedby");
  expect(describedBy).toBeTruthy();
  expect(describedBy?.split(" ").map((id) => document.getElementById(id))).toContain(result);
  await waitFor(() => expect(document.activeElement).toBe(done));
  for (const label of ["当前密码", "新密码", "确认新密码"]) expect(context.dialog.queryByLabelText(label)).toBeNull();
  expect(context.dialogElement.querySelector("form")).toBeNull();
  return done;
}

async function expectClosedAndReopenEmpty(context: Context) {
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(document.activeElement).toBe(context.trigger);
  expect(screen.queryByText("密码已修改。")).toBeNull();
  await userEvent.click(context.trigger);
  const dialog = within(await screen.findByRole("dialog", { name: "修改密码" }));
  expect(document.activeElement).toBe(dialog.getByRole("heading", { level: 2, name: "修改密码" }));
  for (const label of ["当前密码", "新密码", "确认新密码"]) expect(dialog.getByLabelText<HTMLInputElement>(label).value).toBe("");
  expect(dialog.queryByRole("button", { name: "完成" })).toBeNull();
  expect(dialog.queryByText("密码已修改。")).toBeNull();
  expect(dialog.queryByRole("alert")).toBeNull();
}

describe("V1/V5：身份区修改密码", () => {
  it("成功保留会话代次与缓存，焦点到结果态完成按钮；点击后关闭归还焦点，再打开空表单", async () => {
    const context = await openPasswordChange();
    const { runtime, api, session, channel, history } = context;
    const generation = runtime.session.currentGeneration();
    const sessionState = runtime.session.getSnapshot();
    const cache = runtime.queryClient.getQueryCache().getAll().map((query) => ({ query, data: query.state.data }));
    const reads = api.requests.filter((request) => request.method === "GET").map((request) => request.path);
    const invalidation = vi.spyOn(runtime.router, "invalidate");
    const clear = vi.spyOn(runtime.queryClient, "clear");
    const broadcasts = channel.notifyChanged.mock.calls.length;
    await fill(context);
    await submit(context);
    const done = await expectPasswordChanged(context);

    expect(history.location.pathname).toBe("/members");
    expect(screen.getByRole("heading", { level: 1, name: "成员" })).toBeTruthy();
    expect(runtime.session.currentGeneration()).toBe(generation);
    expect(runtime.session.getSnapshot()).toBe(sessionState);
    expect(session.current).toBe("curator");
    expect(session.accounts.curator?.password).toBe(newPassword);
    expect(api.requests.filter((request) => request.method === "GET").map((request) => request.path)).toEqual(reads);
    for (const { query, data } of cache) {
      expect(runtime.queryClient.getQueryCache().get(query.queryHash)).toBe(query);
      expect(query.state.data).toBe(data);
      expect(query.state.isInvalidated).toBe(false);
    }
    expect(clear).not.toHaveBeenCalled();
    expect(invalidation).not.toHaveBeenCalled();
    expect(channel.notifyChanged).toHaveBeenCalledTimes(broadcasts);
    expect(api.requestsTo("POST", passwordPath)).toHaveLength(1);
    const request = api.requestsTo("POST", passwordPath)[0];
    expect(request?.body).toEqual({ current_password: oldPassword, new_password: newPassword });
    expect(request?.headers.get("Idempotency-Key")).toBeNull();
    expect(request?.init.credentials).toBe("same-origin");
    expect(document.body.textContent).not.toContain(newPassword);
    await userEvent.click(done);
    await expectClosedAndReopenEmpty(context);
    expect(runtime.session.currentGeneration()).toBe(generation);
    expect(runtime.session.getSnapshot()).toBe(sessionState);
    for (const { query, data } of cache) {
      expect(runtime.queryClient.getQueryCache().get(query.queryHash)).toBe(query);
      expect(query.state.data).toBe(data);
      expect(query.state.isInvalidated).toBe(false);
    }
    expect(clear).not.toHaveBeenCalled();
    expect(invalidation).not.toHaveBeenCalled();
    expect(channel.notifyChanged).toHaveBeenCalledTimes(broadcasts);
    expect(api.requestsTo("POST", passwordPath)).toHaveLength(1);
  });

  it("403 当前密码错误关联字段、聚焦且保留输入，不退出登录；改对后可再次提交", async () => {
    const context = await openPasswordChange();
    const generation = context.runtime.session.currentGeneration();
    await fill(context, "wrong-password");
    await submit(context);
    await context.dialog.findByText("当前密码不对。");
    expectFieldError(context, "当前密码", "当前密码不对。");
    expect(context.dialog.getByLabelText<HTMLInputElement>("当前密码").value).toBe("wrong-password");
    expect(context.dialog.getByLabelText<HTMLInputElement>("新密码").value).toBe(newPassword);
    expect(context.dialog.getByLabelText<HTMLInputElement>("确认新密码").value).toBe(newPassword);
    expect(context.dialog.queryByRole("alert")).toBeNull();
    expect(context.history.location.pathname).toBe("/members");
    expect(context.runtime.session.currentGeneration()).toBe(generation);
    expect(context.session.current).toBe("curator");
    expect(context.session.accounts.curator?.password).toBe(oldPassword);
    await userEvent.clear(context.dialog.getByLabelText("当前密码"));
    await userEvent.type(context.dialog.getByLabelText("当前密码"), oldPassword);
    expect(context.dialog.queryByText("当前密码不对。")).toBeNull();
    await submit(context);
    await expectPasswordChanged(context);
    expect(context.runtime.session.currentGeneration()).toBe(generation);
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(2);
  });

  it("400 invalid_input 只提示新密码规则，不显示账号规则或后端原文", async () => {
    const context = await openPasswordChange();
    context.api.on("POST", passwordPath, () => sampleResponse("errors/invalid-input-password"));
    await fill(context);
    await submit(context);
    await context.dialog.findByText(newPasswordRuleMessage);
    expectFieldError(context, "新密码", newPasswordRuleMessage);
    expect(context.dialog.queryByRole("alert")).toBeNull();
    expect(context.dialog.getByLabelText<HTMLInputElement>("当前密码").value).toBe(oldPassword);
    expect(context.dialog.queryByText(/用户名不能为空|新密码需要 12 到 72/)).toBeNull();
    expect(context.history.location.pathname).toBe("/members");
  });

  it.each([
    ["网络失败", () => { throw new TypeError("响应中断"); }],
    ["503", () => sampleResponse("errors/database-unavailable")],
    ["无效成功响应", () => jsonResponse(200, { password_changed: false })],
  ])("%s 标为结果未确认，保留输入，不自动重试", async (_name, response) => {
    const context = await openPasswordChange();
    context.api.on("POST", passwordPath, response);
    await fill(context);
    await submit(context);
    const alert = await context.dialog.findByRole("alert");
    expect(within(alert).getByText("结果未确认")).toBeTruthy();
    expect(context.dialog.getByLabelText<HTMLInputElement>("当前密码").value).toBe(oldPassword);
    expect(context.dialog.getByLabelText<HTMLInputElement>("新密码").value).toBe(newPassword);
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(1);
    expect(context.runtime.session.getSnapshot().status).toBe("authenticated");
    context.api.on("POST", passwordPath, () => sampleResponse("auth/password-changed"));
    await submit(context);
    await expectPasswordChanged(context);
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(2);
  });

  it("结果未确认提示退出后用新密码登录核对", async () => {
    const context = await openPasswordChange();
    context.api.on("POST", passwordPath, () => { throw new TypeError("响应中断"); });
    await fill(context);
    await submit(context);
    const alert = await context.dialog.findByRole("alert");
    expect(within(alert).getByText("结果未确认")).toBeTruthy();
    expect(within(alert).getByText("不确定改好了没有，可以退出后用新密码登录试试。")).toBeTruthy();
  });

  it("真正的 401 unauthorized 结束会话、清缓存并回到登录页", async () => {
    const context = await openPasswordChange();
    const generation = context.runtime.session.currentGeneration();
    context.api.on("POST", passwordPath, () => sampleResponse("errors/unauthorized"));
    await fill(context);
    await submit(context);
    await screen.findByRole("heading", { name: "登录" });
    expect(context.history.location.pathname).toBe("/login");
    expect(context.runtime.session.currentGeneration()).toBeGreaterThan(generation);
    expect(context.runtime.session.getSnapshot().status).toBe("anonymous");
    expect(context.runtime.queryClient.getQueryCache().getAll()).toHaveLength(0);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("提交中禁止取消与 Esc；成功后可用 Esc 关闭归还焦点，重新打开空表单", async () => {
    const context = await openPasswordChange();
    const gate = deferred<Response>();
    context.api.on("POST", passwordPath, () => gate.promise);
    await fill(context);
    await submit(context);
    expect(context.dialog.getByRole<HTMLButtonElement>("button", { name: "取消" }).disabled).toBe(true);
    expect(context.dialog.getByRole("button", { name: "正在修改…" }).getAttribute("aria-busy")).toBe("true");
    // jsdom 不把 Esc 转成原生 cancel；显式派发浏览器会发出的可取消事件。
    const cancel = new Event("cancel", { cancelable: true });
    fireEvent(context.dialogElement, cancel);
    await userEvent.keyboard("{Escape}");
    expect(cancel.defaultPrevented).toBe(true);
    expect(screen.getByRole("dialog", { name: "修改密码" })).toBe(context.dialogElement);
    const form = context.dialog.getByRole("button", { name: "正在修改…" }).closest("form");
    if (form === null) throw new Error("缺少改密码表单");
    fireEvent.submit(form);
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(1);
    await act(async () => { gate.resolve(sampleResponse("auth/password-changed")); });
    await expectPasswordChanged(context);
    const successCancel = new Event("cancel", { cancelable: true });
    fireEvent(context.dialogElement, successCancel);
    expect(successCancel.defaultPrevented).toBe(true);
    await expectClosedAndReopenEmpty(context);
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(1);
  });

  it("本地校验按当前密码、新密码、确认依次聚焦，失败不发请求", async () => {
    const context = await openPasswordChange();
    await submit(context);
    expectFieldError(context, "当前密码", "请输入当前密码。");
    for (const [value, message] of [["a".repeat(11), "密码至少要 12 位。"], ["a".repeat(73), "密码太长了，请短一些。"], ["password1234密", "密码只能用英文字母、数字和符号。"]] as const) {
      await fill(context, oldPassword, value);
      await submit(context);
      expectFieldError(context, "新密码", message);
    }
    await fill(context, oldPassword, newPassword, "different-password");
    await submit(context);
    expectFieldError(context, "确认新密码", "两次输入的密码不一致。");
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(0);
  });

  it("密码管理器字段指向当前账号，取消或 Esc 丢弃输入，再打开没有旧秘密", async () => {
    const context = await openPasswordChange();
    expect(document.activeElement).toBe(context.dialog.getByRole("heading", { level: 2, name: "修改密码" }));
    expect(context.dialog.getByLabelText("当前密码").getAttribute("autocomplete")).toBe("current-password");
    for (const label of ["新密码", "确认新密码"]) expect(context.dialog.getByLabelText(label).getAttribute("autocomplete")).toBe("new-password");
    const username = context.dialogElement.querySelector<HTMLInputElement>('input[autocomplete="username"]');
    expect(username?.value).toBe("curator");
    expect(username?.hidden).toBe(true);
    expect(username?.readOnly).toBe(true);
    await fill(context);
    fireEvent(context.dialogElement, new Event("cancel", { cancelable: true }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await userEvent.click(screen.getByRole("button", { name: "修改密码" }));
    const dialog = within(await screen.findByRole("dialog"));
    for (const label of ["当前密码", "新密码", "确认新密码"]) expect(dialog.getByLabelText<HTMLInputElement>(label).value).toBe("");
    expect(context.api.requestsTo("POST", passwordPath)).toHaveLength(0);
  });
});
