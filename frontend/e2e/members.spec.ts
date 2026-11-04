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
    await expect(dialog).toContainText("立即退出登录");
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
