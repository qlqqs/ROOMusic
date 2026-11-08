import { changedPassword, expectLogoutInViewport, expectPasswordChanged, fillPasswordChange, passwordDialog } from "./support/account-password";
import { artistKeyActivate as keyActivate, artistTabTo as keyboardTabTo } from "./support/artists";
import { expectFocusStaysInDialog } from "./support/keyboard";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import { logoutFromMenu, openSettings, userTrigger } from "./support/settings";

test.describe("初始化、登录与退出", () => {
  test("初始化后自动登录：创建管理员 → 登录 → 概览，密码不出现在页面上", async ({ page, backend }) => {
    const mock = await backend.start({ setupRequired: true });
    await page.goto("/");
    await expect(heading(page, "创建管理员")).toBeVisible();
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码", { exact: true }).fill("correct horse battery");
    await page.getByLabel("确认密码").fill("correct horse battery");
    await page.getByRole("button", { name: "创建管理员并登录" }).click();

    await expect(heading(page, "概览")).toBeVisible();
    expect(mock.callsTo("POST", "/api/v1/setup/admin")).toHaveLength(1);
    expect(mock.callsTo("POST", "/api/v1/auth/login")).toHaveLength(1);
    await expect(page.locator("body")).not.toContainText("correct horse battery");
  });

  test("登录、刷新后保持、退出回到登录页且地址不带回跳", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/");
    await expect(heading(page, "登录")).toBeVisible();
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("curator-password");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(heading(page, "概览")).toBeVisible();
    await expect(page.getByRole("region", { name: "当前用户" })).toContainText("curator");

    await page.reload();
    await expect(heading(page, "概览")).toBeVisible();

    await logoutFromMenu(page);
    await expect(heading(page, "登录")).toBeVisible();
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByText("已退出登录。")).toBeVisible();
  });

  test("错误密码：显示提示与请求 ID，不进入应用", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/");
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("wrong password");
    await page.getByRole("button", { name: "登录" }).click();
    const alert = page.getByRole("alert");
    await expect(alert).toContainText("用户名或密码错误");
    await expect(alert).toContainText("请求 ID");
    await expect(heading(page, "登录")).toBeVisible();
  });

  test("深层地址：未登录先去登录页，登录后回到原地址；不合法的回跳被丢弃", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/members?tab=1");
    await expect(heading(page, "登录")).toBeVisible();
    await expect(page).toHaveURL(/\/login\?redirect=%2Fsettings%2Fmembers%3Ftab%3D1$/);
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("curator-password");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(heading(page, "成员")).toBeVisible();
    await expect(page).toHaveURL(/\/settings\/members\?tab=1$/);

    await logoutFromMenu(page);
    await page.goto("/login?redirect=//evil.example/x");
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("curator-password");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(heading(page, "概览")).toBeVisible();
    await expect(page).toHaveURL(/127\.0\.0\.1:\d+\/$/);
  });

  test("会话在使用中途失效：下一次请求得到 401，回到登录页并提示，登录后回到当时的页面", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();

    await openSettings(page);
    mock.expireSession();
    await page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link", { name: "扫描目录" }).click();
    await expect(heading(page, "登录")).toBeVisible();
    await expect(page.getByText("登录已失效，请重新登录。")).toBeVisible();
    await expect(page).toHaveURL(/redirect=%2Fsettings%2Flibrary/);

    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("curator-password");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(heading(page, "扫描目录")).toBeVisible();
  });

  test("另一个标签页退出后，本页跟随回到登录页", async ({ page, context, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();
    const other = await context.newPage();
    await other.goto("/");
    await expect(heading(other, "概览")).toBeVisible();

    await logoutFromMenu(other);
    await expect(heading(other, "登录")).toBeVisible();
    await expect(heading(page, "登录")).toBeVisible();
  });

  test("启动时服务不可用（503）：整页错误可重试，重试成功后进入登录页", async ({ page, backend }) => {
    await backend.start({ failSetupStatusTimes: 1 });
    await page.goto("/");
    await expect(heading(page, "暂时无法使用")).toBeVisible();
    await expect(page.getByRole("alert")).toContainText("服务暂时不可用");
    await page.getByRole("button", { name: "重试" }).click();
    await expect(heading(page, "登录")).toBeVisible();
  });
});

test.describe("权限", () => {
  test("成员访问管理页：显示“需要管理员权限”，不请求管理接口，导航里没有管理入口", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    await page.goto("/settings/library");
    await expect(heading(page, "需要管理员权限")).toBeVisible();
    // 艺人信息就绪时也显示艺人入口；普通成员仍没有扫描目录与成员等管理入口。
    await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link")).toHaveText(["概览", "专辑", "艺人", "我喜欢的音乐", "我的收藏", "播放历史", "我的歌单"]);
    expect(mock.callsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
    expect(mock.callsTo("GET", "/api/v1/library-root-operations")).toHaveLength(0);
  });

  test("接口返回 403：只拒绝当前操作，会话与导航保留", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.forbid.add("GET /api/v1/library-roots");
    await page.goto("/settings/library");
    await expect(heading(page, "扫描目录")).toBeVisible();
    await expect(page.getByRole("alert").filter({ hasText: "你没有权限执行此操作。" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "主导航" })).toBeVisible();
    await expect(page.getByRole("region", { name: "当前用户" })).toContainText("curator");
  });

  test("未知路径：已登录显示在外壳里的“页面不存在”，未登录先去登录", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/nowhere");
    await expect(heading(page, "页面不存在")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "主导航" })).toBeVisible();
  });
});

test.describe("账户密码：自助修改", () => {
  test("纯键盘输错当前密码后字段报错，再改对成功且不掉线", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/account?tab=security");
    await expect(heading(page, "账号")).toBeVisible();
    const originalURL = page.url();
    const meReads = mock.callsTo("GET", "/api/v1/auth/me").length;
    const trigger = page.getByRole("button", { name: "修改密码", exact: true });
    await keyActivate(page, trigger);
    const dialog = passwordDialog(page);
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("heading", { name: "修改密码", exact: true })).toBeFocused();
    const current = dialog.getByLabel("当前密码", { exact: true });
    const next = dialog.getByLabel("新密码", { exact: true });
    const confirm = dialog.getByLabel("确认新密码", { exact: true });
    await keyboardTabTo(page, current);
    await page.keyboard.type("wrong-current-password");
    await keyboardTabTo(page, next);
    await page.keyboard.type(changedPassword);
    await keyboardTabTo(page, confirm);
    await page.keyboard.type(changedPassword);
    await keyActivate(page, dialog.getByRole("button", { name: "修改密码", exact: true }));
    await expect(dialog.getByText("当前密码不对。", { exact: true })).toBeVisible();
    await expect(current).toHaveAttribute("aria-invalid", "true");
    await expect(current).toHaveAccessibleDescription("当前密码不对。");
    await expect(current).toBeFocused();
    await expect(next).toHaveValue(changedPassword);
    await expect(confirm).toHaveValue(changedPassword);
    await expect(page).toHaveURL(originalURL);
    expect(mock.session).toBe("curator");
    expect(mock.accounts.get("curator")?.password).toBe("curator-password");

    await page.keyboard.press("ControlOrMeta+A");
    await page.keyboard.type("curator-password");
    await keyActivate(page, dialog.getByRole("button", { name: "修改密码", exact: true }));
    const done = await expectPasswordChanged(page);
    await expect(page).toHaveURL(originalURL);
    await keyActivate(page, done);
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(page.getByRole("region", { name: "当前用户" })).not.toContainText("密码已修改。");
    await expect(heading(page, "账号")).toBeVisible();
    await expect(page).toHaveURL(originalURL);
    expect(mock.session).toBe("curator");
    expect(mock.accounts.get("curator")?.password).toBe(changedPassword);
    expect(mock.callsTo("GET", "/api/v1/auth/me")).toHaveLength(meReads);
    expect(mock.callsTo("POST", "/api/v1/auth/logout")).toHaveLength(0);
    const calls = mock.callsTo("POST", "/api/v1/auth/password");
    expect(calls.map(({ body }) => body)).toEqual([
      { current_password: "wrong-current-password", new_password: changedPassword },
      { current_password: "curator-password", new_password: changedPassword },
    ]);
    expect(calls.every(({ idempotencyKey }) => idempotencyKey === null)).toBe(true);
    await expect(page.locator("body")).not.toContainText(changedPassword);
    await page.reload();
    await expect(heading(page, "账号")).toBeVisible();
    await expect(page.getByRole("region", { name: "当前用户" })).toContainText("curator");
  });

  for (const closeWith of ["完成", "Esc"] as const) {
    test(`成功结果留在对话框，${closeWith}关闭回到入口、重开空表单且用户按钮完整在视口`, async ({ page, backend }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      const mock = await backend.start({ signedInAs: "curator" });
      await page.goto("/settings/account");
      await expect(heading(page, "账号")).toBeVisible();
      await expectLogoutInViewport(page);
      const trigger = page.getByRole("button", { name: "修改密码", exact: true });
      await keyActivate(page, trigger);
      await fillPasswordChange(page);
      await keyActivate(page, passwordDialog(page).getByRole("button", { name: "修改密码", exact: true }));
      const done = await expectPasswordChanged(page);
      if (closeWith === "完成") await keyActivate(page, done);
      else await page.keyboard.press("Escape");
      await expect(passwordDialog(page)).toBeHidden();
      await expect(trigger).toBeFocused();
      await expect(page.getByRole("region", { name: "当前用户" })).not.toContainText("密码已修改。");
      await expect(page.getByText("密码已修改。", { exact: true })).toHaveCount(0);
      await expectLogoutInViewport(page);
      await page.keyboard.press("Enter");
      const reopened = passwordDialog(page);
      await expect(reopened.getByRole("heading", { name: "修改密码", exact: true })).toBeFocused();
      for (const label of ["当前密码", "新密码", "确认新密码"]) await expect(reopened.getByLabel(label, { exact: true })).toHaveValue("");
      await expect(reopened.getByRole("button", { name: "完成", exact: true })).toHaveCount(0);
      await expect(reopened.getByText("密码已修改。", { exact: true })).toHaveCount(0);
      await page.keyboard.press("Escape");
      await expect(trigger).toBeFocused();
      await expectLogoutInViewport(page);
      expect(mock.callsTo("POST", "/api/v1/auth/password")).toHaveLength(1);
      expect(mock.session).toBe("curator");
    });
  }

  for (const invalid of [
    { name: "缺少当前密码", current: "", password: changedPassword, confirmation: changedPassword, field: "当前密码", error: "请输入当前密码。" },
    { name: "新密码11位", current: "curator-password", password: "x".repeat(11), confirmation: "x".repeat(11), field: "新密码", error: "密码至少要 12 位。" },
    { name: "新密码73位", current: "curator-password", password: "x".repeat(73), confirmation: "x".repeat(73), field: "新密码", error: "密码太长了，请短一些。" },
    { name: "新密码含中文", current: "curator-password", password: "new-password-中文", confirmation: "new-password-中文", field: "新密码", error: "密码只能用英文字母、数字和符号。" },
    { name: "确认不一致", current: "curator-password", password: changedPassword, confirmation: "different-password", field: "确认新密码", error: "两次输入的密码不一致。" },
  ]) {
    test(`${invalid.name}本地拦截，不发改密码请求`, async ({ page, backend }) => {
      const mock = await backend.start({ signedInAs: "curator" });
      await page.goto("/settings/account");
      await keyActivate(page, page.getByRole("button", { name: "修改密码", exact: true }));
      const dialog = passwordDialog(page);
      await dialog.getByLabel("当前密码", { exact: true }).fill(invalid.current);
      await dialog.getByLabel("新密码", { exact: true }).fill(invalid.password);
      await dialog.getByLabel("确认新密码", { exact: true }).fill(invalid.confirmation);
      await dialog.getByRole("button", { name: "修改密码", exact: true }).click();
      await expect(dialog.getByText(invalid.error, { exact: true })).toBeVisible();
      await expect(dialog.getByLabel(invalid.field, { exact: true })).toBeFocused();
      await expect(dialog.getByLabel(invalid.field, { exact: true })).toHaveAttribute("aria-invalid", "true");
      expect(mock.callsTo("POST", "/api/v1/auth/password")).toHaveLength(0);
      expect(mock.accounts.get("curator")?.password).toBe("curator-password");
    });
  }

  test("窄屏用户菜单进入账号栏，Esc仅关密码对话框且焦点回到入口", async ({ page, backend }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    const menuTrigger = page.getByRole("button", { name: "菜单", exact: true });
    await openSettings(page);
    const drawer = page.getByRole("dialog", { name: "菜单", exact: true });
    const trigger = page.getByRole("button", { name: "修改密码", exact: true });
    await keyActivate(page, trigger);
    const dialog = passwordDialog(page);
    await expect(dialog).toBeVisible();
    await expectFocusStaysInDialog(page, dialog, 8);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(drawer).toBeHidden();
    await expect(page.locator("dialog[open]")).toHaveCount(0);
    await expect(trigger).toBeFocused();
    expect(mock.callsTo("POST", "/api/v1/auth/password")).toHaveLength(0);
    await keyActivate(page, menuTrigger);
    await expect(drawer).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(drawer).toBeHidden();
    await expect(menuTrigger).toBeFocused();
    await expect(heading(page, "账号")).toBeVisible();
  });
  test("窄屏用户菜单进入账号栏，成功后Esc关闭且入口焦点保留、重开为空", async ({ page, backend }) => {
    await page.setViewportSize({ width: 375, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await openSettings(page);
    const drawer = page.getByRole("dialog", { name: "菜单", exact: true });
    const trigger = page.getByRole("button", { name: "修改密码", exact: true });
    await keyActivate(page, trigger);
    await fillPasswordChange(page);
    await keyActivate(page, passwordDialog(page).getByRole("button", { name: "修改密码", exact: true }));
    await expectPasswordChanged(page);
    await page.keyboard.press("Escape");
    await expect(passwordDialog(page)).toBeHidden();
    await expect(drawer).toBeHidden();
    await expect(page.locator("dialog[open]")).toHaveCount(0);
    await expect(trigger).toBeFocused();
    await expect(page.getByRole("main")).not.toContainText("密码已修改。");
    await page.keyboard.press("Enter");
    for (const label of ["当前密码", "新密码", "确认新密码"]) await expect(passwordDialog(page).getByLabel(label, { exact: true })).toHaveValue("");
    await expect(passwordDialog(page).getByRole("button", { name: "完成", exact: true })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(drawer).toBeHidden();
    await expect(trigger).toBeFocused();
    expect(mock.callsTo("POST", "/api/v1/auth/password")).toHaveLength(1);
    expect(mock.session).toBe("curator");
  });

});

test.describe("侧栏用户菜单", () => {
  test("向上展开、完整键盘循环和 Esc 恢复焦点，设置分栏只在设置页出现", async ({ page, backend }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();
    const trigger = userTrigger(page);
    await expect(trigger).toHaveAccessibleName("curator 管理员");
    await expect(trigger.locator('[aria-hidden="true"]').filter({ hasText: "C" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: /^(扫描目录|成员|操作记录|备份)$/ })).toHaveCount(0);
    await keyActivate(page, trigger, "ArrowDown");
    const menu = page.getByRole("menu", { name: "账号", exact: true });
    const settings = menu.getByRole("menuitem", { name: "设置", exact: true });
    const logout = menu.getByRole("menuitem", { name: "退出登录", exact: true });
    await expect(menu.getByRole("menuitem")).toHaveText(["设置", "退出登录"]);
    await expect(settings).toBeFocused();
    const menuBounds = await menu.boundingBox();
    const triggerBounds = await trigger.boundingBox();
    if (menuBounds === null || triggerBounds === null) throw new Error("菜单与用户按钮必须有边界");
    expect(menuBounds.y + menuBounds.height).toBeLessThanOrEqual(triggerBounds.y);
    expect(Math.abs(menuBounds.width - Math.max(triggerBounds.width, 200))).toBeLessThan(2);
    await page.keyboard.press("ArrowUp");
    await expect(logout).toBeFocused();
    await page.keyboard.press("Home");
    await expect(settings).toBeFocused();
    await page.keyboard.press("End");
    await expect(logout).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(settings).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    await openSettings(page);
    await expect(trigger).toHaveAttribute("data-current", "");
    await expect(page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link")).toHaveText(["账号", "扫描目录", "成员", "操作记录", "备份"]);
  });

  test("退出失败留在当前页面，菜单关闭且提示重试后才真正退出", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/albums?q=Album");
    await expect(heading(page, "专辑")).toBeVisible();
    const originalURL = page.url();
    await page.route("**/api/v1/auth/logout", (route) => route.fulfill({ status: 503, json: { error: { code: "database_unavailable", message: "服务暂时不可用" }, request_id: "logout-test" } }), { times: 1 });
    await logoutFromMenu(page);
    const identity = page.getByRole("region", { name: "当前用户" });
    await expect(identity.getByRole("alert")).toContainText("退出失败");
    await expect(identity.getByRole("button", { name: "重试", exact: true })).toBeVisible();
    await expect(page.getByRole("menu", { name: "账号", exact: true })).toBeHidden();
    await expect(userTrigger(page)).toBeFocused();
    await expect(page).toHaveURL(originalURL);
    expect(mock.session).toBe("curator");
    await identity.getByRole("button", { name: "重试", exact: true }).click();
    await expect(heading(page, "登录")).toBeVisible();
    expect(mock.session).toBeNull();
  });
});
