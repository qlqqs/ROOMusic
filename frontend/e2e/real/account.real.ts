import { expect, test, type APIRequestContext, type BrowserContext } from "@playwright/test";
import { decodeViewer } from "../../src/features/auth/api/decoders";
import { admin, body, decodeError, decodeID, decodeSetup, origin } from "./support";

const originalPassword = "AccountOriginalPassword42";
const changedPassword = "AccountChangedPassword42";
const resetPassword = "AccountResetPassword42";

// 身份写操作不带幂等键；不复用治理操作的 post 辅助函数。
function identityPost(api: APIRequestContext, path: string, data: unknown) {
  return api.post(path, { data, headers: { Origin: origin } });
}

async function signIn(api: APIRequestContext, username: string, password: string) {
  return decodeViewer(await body(await identityPost(api, "/api/v1/auth/login", { username, password })));
}

async function createMember(api: APIRequestContext) {
  const setup = decodeSetup(await body(await api.get("/api/v1/setup/status")));
  if (setup.setup_required) await body(await identityPost(api, "/api/v1/setup/admin", admin));
  const administrator = await signIn(api, admin.username, admin.password);
  const username = `account-${crypto.randomUUID().slice(0, 8)}`;
  const member = decodeID(await body(await identityPost(api, "/api/v1/users", { username, password: originalPassword })));
  return { ...member, username, administrator };
}

async function expectRejectedLogin(api: APIRequestContext, username: string, password: string) {
  const response = await identityPost(api, "/api/v1/auth/login", { username, password });
  expect(response.status()).toBe(401);
  const input: unknown = await response.json();
  expect(decodeError(input).error.code).toBe("invalid_credentials");
}

async function expectSessionEnded(context: BrowserContext) {
  const response = await context.request.get("/api/v1/auth/me");
  expect(response.status()).toBe(401);
  const input: unknown = await response.json();
  expect(decodeError(input).error.code).toBe("unauthorized");
}

async function disableMember(api: APIRequestContext, id: string) {
  const response = await api.patch(`/api/v1/users/${id}`, { data: { disabled: true }, headers: { Origin: origin } });
  await body(response);
}

test("真实账户 V1：自助改密码保留当前设备、撤销另一设备，旧密码失效且新密码能登录", async ({ browser, request, baseURL }) => {
  if (baseURL === undefined) throw new Error("缺少隔离服务地址");
  const member = await createMember(request);
  const contexts: BrowserContext[] = [];
  try {
    const a = await browser.newContext({ baseURL });
    contexts.push(a);
    const b = await browser.newContext({ baseURL });
    contexts.push(b);
    const probe = await browser.newContext({ baseURL });
    contexts.push(probe);
    await signIn(a.request, member.username, originalPassword);
    await signIn(b.request, member.username, originalPassword);
    const page = await a.newPage();
    await page.goto("/albums");
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    const cookies = await a.cookies();
    await page.getByRole("button", { name: "修改密码", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "修改密码", exact: true });
    await dialog.getByLabel("当前密码", { exact: true }).fill(originalPassword);
    await dialog.getByLabel("新密码", { exact: true }).fill(changedPassword);
    await dialog.getByLabel("确认新密码", { exact: true }).fill(changedPassword);
    const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/api/v1/auth/password"));
    await dialog.getByRole("button", { name: "修改密码", exact: true }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toEqual({ current_password: originalPassword, new_password: changedPassword });
    expect(await response.request().headerValue("Idempotency-Key")).toBeNull();
    expect(await response.headerValue("Set-Cookie")).toBeNull();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("密码已修改。", { exact: true })).toBeVisible();
    const done = dialog.getByRole("button", { name: "完成", exact: true });
    await expect(done).toBeFocused();
    await expect(done).toHaveAccessibleDescription("密码已修改。");
    await expect(dialog.getByRole("status")).toHaveCount(0);
    await done.click();
    await expect(dialog).toBeHidden();
    const trigger = page.getByRole("button", { name: "修改密码", exact: true });
    await expect(trigger).toBeFocused();
    await expect(page.getByRole("region", { name: "当前用户" })).not.toContainText("密码已修改。");
    await trigger.click();
    for (const label of ["当前密码", "新密码", "确认新密码"]) await expect(dialog.getByLabel(label, { exact: true })).toHaveValue("");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(page).toHaveURL(/\/albums$/);
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    expect(decodeViewer(await body(await a.request.get("/api/v1/auth/me"))).id).toBe(member.id);
    expect(await a.cookies()).toEqual(cookies);
    await expectSessionEnded(b);
    await expectRejectedLogin(probe.request, member.username, originalPassword);
    expect((await signIn(probe.request, member.username, changedPassword)).id).toBe(member.id);
    await page.reload();
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
  } finally {
    try {
      await disableMember(request, member.id);
    } finally {
      await Promise.all(contexts.map((context) => context.close()));
    }
  }
});

test("真实账户 V2：管理员在成员页重置密码，撤销全部成员会话，普通成员不能重置且自己没有入口", async ({ browser, page, request, baseURL }) => {
  if (baseURL === undefined) throw new Error("缺少隔离服务地址");
  const member = await createMember(request);
  const contexts: BrowserContext[] = [];
  try {
    const a = await browser.newContext({ baseURL });
    contexts.push(a);
    const b = await browser.newContext({ baseURL });
    contexts.push(b);
    const probe = await browser.newContext({ baseURL });
    contexts.push(probe);
    await signIn(a.request, member.username, originalPassword);
    await signIn(b.request, member.username, originalPassword);
    // 越权探测只针对临时成员；即使权限校验回归，也不能改动共享管理员的密码。
    const forbidden = await identityPost(a.request, `/api/v1/users/${member.id}/password`, { password: resetPassword });
    expect(forbidden.status()).toBe(403);
    const forbiddenBody: unknown = await forbidden.json();
    expect(decodeError(forbiddenBody).error.code).toBe("forbidden");
    expect(decodeViewer(await body(await a.request.get("/api/v1/auth/me"))).id).toBe(member.id);
    await signIn(page.request, admin.username, admin.password);
    await page.goto("/members");
    const table = page.getByRole("table", { name: "成员列表" });
    const selfRow = table.getByRole("row").filter({ has: page.getByText(admin.username, { exact: true }) });
    await expect(selfRow).toHaveCount(1);
    await expect(selfRow.getByRole("button", { name: /^重置成员 / })).toHaveCount(0);
    await table.getByRole("button", { name: `重置成员 ${member.username} 的密码`, exact: true }).click();
    const dialog = page.getByRole("dialog", { name: `重置“${member.username}”的密码`, exact: true });
    await dialog.getByLabel("新密码", { exact: true }).fill(resetPassword);
    await dialog.getByLabel("确认新密码", { exact: true }).fill(resetPassword);
    const responsePromise = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/api/v1/users/${member.id}/password`));
    await dialog.getByRole("button", { name: "重置密码", exact: true }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toEqual({ password: resetPassword });
    expect(await response.request().headerValue("Idempotency-Key")).toBeNull();
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("status").filter({ hasText: `已重置“${member.username}”的密码` })).toBeVisible();
    await expect(page).toHaveURL(/\/members$/);
    await expectSessionEnded(a);
    await expectSessionEnded(b);
    await expectRejectedLogin(probe.request, member.username, originalPassword);
    expect((await signIn(probe.request, member.username, resetPassword)).id).toBe(member.id);
    expect(decodeViewer(await body(await page.request.get("/api/v1/auth/me"))).id).toBe(member.administrator.id);
  } finally {
    try {
      await disableMember(request, member.id);
    } finally {
      await Promise.all(contexts.map((context) => context.close()));
    }
  }
});
