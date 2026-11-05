import { changedPassword, memberRow } from "./support/account-password";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import type { Page } from "@playwright/test";

const membersTable = (page: Page) => page.getByRole("table", { name: "成员列表" });

async function fillForm(page: Page, username: string, password: string, confirmation = password) {
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByLabel("确认密码").fill(confirmation);
}

test.describe("成员管理", () => {
  test("新建成员：校验不合规的输入，创建成功后出现在列表，重复用户名显示在字段旁，请求没有幂等键", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/members");
    await expect(heading(page, "成员")).toBeVisible();

    await page.getByRole("button", { name: "创建成员" }).click();
    await expect(page.getByText("请输入用户名。")).toBeVisible();
    await expect(page.getByLabel("用户名")).toBeFocused();
    expect(mock.callsTo("POST", "/api/v1/users")).toHaveLength(0);

    await fillForm(page, "newbie", "correct horse battery");
    await page.getByRole("button", { name: "创建成员" }).click();
    await expect(page.getByText(/已创建成员“newbie”/)).toBeVisible();
    await expect(membersTable(page).getByRole("row", { name: /newbie/ })).toContainText("成员");
    await expect(page.getByLabel("用户名")).toHaveValue("");
    await expect(page.locator("main")).not.toContainText("correct horse battery");
    const [created] = mock.callsTo("POST", "/api/v1/users");
    expect(created?.body).toEqual({ username: "newbie", password: "correct horse battery" });
    expect(created?.idempotencyKey).toBeNull();

    await fillForm(page, "listener", "correct horse battery");
    await page.getByRole("button", { name: "创建成员" }).click();
    await expect(page.getByText("用户名已存在，请换一个。")).toBeVisible();
    await expect(page.getByLabel("用户名")).toHaveValue("listener");
  });

  test("停用 → 重新启用 → 撤销会话：停用与撤销要确认，重新启用直接执行", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/members");
    const row = (name: string) => membersTable(page).getByRole("row", { name: new RegExp(name) });

    await row("listener").getByRole("button", { name: "停用成员 listener" }).click();
    const dialog = page.getByRole("dialog", { name: "停用成员“listener”？" });
    await expect(dialog.getByText("停用后对方会马上退出登录，之后可以重新启用。", { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: "停用成员" }).click();
    await expect(page.getByText(/已停用成员“listener”/)).toBeVisible();
    await expect(row("listener")).toContainText("已停用");
    expect(mock.callsTo("PATCH", /\/users\/[^/]+$/)[0]?.body).toEqual({ disabled: true });

    await row("listener").getByRole("button", { name: "重新启用成员 listener" }).click();
    await expect(page.getByText(/已重新启用成员“listener”/)).toBeVisible();
    await expect(row("listener")).toContainText("启用");
    await expect(page.getByRole("dialog")).toHaveCount(0);

    await row("listener").getByRole("button", { name: "撤销会话 listener" }).click();
    await page.getByRole("dialog", { name: "撤销成员“listener”的全部会话？" }).getByRole("button", { name: "撤销会话" }).click();
    await expect(page.getByText(/已撤销成员“listener”的全部会话/)).toBeVisible();
    expect(mock.callsTo("POST", /\/sessions\/revoke$/)).toHaveLength(1);
  });

  test("对自己的操作：对话框先提示；停用最后一个管理员被拒绝（409）且会话保留", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/members");
    await membersTable(page).getByRole("button", { name: "停用成员 curator" }).click();
    const dialog = page.getByRole("dialog", { name: "停用成员“curator”？" });
    await expect(dialog).toContainText("这是你自己的账号，确认后你会马上退出登录。");
    await dialog.getByRole("button", { name: "停用成员" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "不能停用最后一个管理员" })).toBeVisible();
    await expect(heading(page, "成员")).toBeVisible();
  });

  test("停用自己（还有别的管理员）：会话被撤销，随后回到登录页", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.users = [...mock.users, { id: "00000000-0000-4000-8000-0000000000aa", username: "second", role: "admin", disabled: false, created_at: "2026-01-01T00:00:00Z" }];
    await page.goto("/members");
    await membersTable(page).getByRole("button", { name: "停用成员 curator" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "停用成员" }).click();
    await expect(heading(page, "登录")).toBeVisible();
    await expect(page.getByRole("table", { name: "成员列表" })).toHaveCount(0);
  });
});

test.describe("账户密码：管理员重置", () => {
  for (const { username, disabled } of [{ username: "listener", disabled: false }, { username: "archivist", disabled: true }]) {
    test(`重置${disabled ? "停用" : "启用"}成员密码，自己无入口且请求只有password无幂等键`, async ({ page, backend }) => {
      const mock = await backend.start({ signedInAs: "curator" });
      await page.goto("/members");
      await expect(memberRow(page, "curator").getByRole("button", { name: /重置/ })).toHaveCount(0);
      const row = memberRow(page, username);
      await expect(row.getByRole("button")).toHaveCount(disabled ? 2 : 3);
      await row.getByRole("button", { name: `重置成员 ${username} 的密码`, exact: true }).click();
      const dialog = page.getByRole("dialog", { name: `重置“${username}”的密码`, exact: true });
      await expect(dialog).toContainText(disabled ? "对方仍是停用状态，重新启用后才能登录。" : "对方需要用新密码重新登录。");
      await dialog.getByLabel("新密码", { exact: true }).fill(changedPassword);
      await dialog.getByLabel("确认新密码", { exact: true }).fill(changedPassword);
      await dialog.getByRole("button", { name: "重置密码", exact: true }).click();
      await expect(dialog).toBeHidden();
      await expect(page.getByRole("status").filter({ hasText: `已重置“${username}”的密码。` })).toBeVisible();
      await expect(row).toContainText(disabled ? "已停用" : "启用");
      expect(mock.users.find((user) => user.username === username)?.disabled).toBe(disabled);
      const calls = mock.callsTo("POST", /\/users\/[^/]+\/password$/);
      expect(calls).toHaveLength(1);
      expect(calls[0]?.path).toBe(`/api/v1/users/${mock.users.find((user) => user.username === username)?.id}/password`);
      expect(calls[0]?.body).toEqual({ password: changedPassword });
      expect(calls[0]?.idempotencyKey).toBeNull();
      await expect(page.locator("body")).not.toContainText(changedPassword);
      await expect(heading(page, "成员")).toBeVisible();
      expect(mock.session).toBe("curator");
    });
  }

  test("重置密码本地校验与取消，不向服务器发送密码", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/members");
    const trigger = memberRow(page, "listener").getByRole("button", { name: "重置成员 listener 的密码", exact: true });
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: "重置“listener”的密码", exact: true });
    for (const { password, confirmation, field, error } of [
      { password: "x".repeat(11), confirmation: "x".repeat(11), field: "新密码", error: "密码至少要 12 位。" },
      { password: "x".repeat(73), confirmation: "x".repeat(73), field: "新密码", error: "密码太长了，请短一些。" },
      { password: "new-password-中文", confirmation: "new-password-中文", field: "新密码", error: "密码只能用英文字母、数字和符号。" },
      { password: changedPassword, confirmation: "different-password", field: "确认新密码", error: "两次输入的密码不一致。" },
    ]) {
      await dialog.getByLabel("新密码", { exact: true }).fill(password);
      await dialog.getByLabel("确认新密码", { exact: true }).fill(confirmation);
      await dialog.getByRole("button", { name: "重置密码", exact: true }).click();
      await expect(dialog.getByText(error, { exact: true })).toBeVisible();
      await expect(dialog.getByLabel(field, { exact: true })).toBeFocused();
      expect(mock.callsTo("POST", /\/users\/[^/]+\/password$/)).toHaveLength(0);
    }
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    await trigger.press("Enter");
    await expect(dialog.getByLabel("新密码", { exact: true })).toHaveValue("");
    await expect(dialog.getByLabel("确认新密码", { exact: true })).toHaveValue("");
  });
});
