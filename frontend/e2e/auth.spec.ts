import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

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

    await page.getByRole("button", { name: "退出登录" }).click();
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
    await expect(page).toHaveURL(/\/login\?redirect=%2Fmembers%3Ftab%3D1$/);
    await page.getByLabel("用户名").fill("curator");
    await page.getByLabel("密码").fill("curator-password");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(heading(page, "成员")).toBeVisible();
    await expect(page).toHaveURL(/\/members\?tab=1$/);

    await page.getByRole("button", { name: "退出登录" }).click();
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

    mock.expireSession();
    await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "扫描目录" }).click();
    await expect(heading(page, "登录")).toBeVisible();
    await expect(page.getByText("登录已失效，请重新登录。")).toBeVisible();
    await expect(page).toHaveURL(/redirect=%2Flibrary/);

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

    await other.getByRole("button", { name: "退出登录" }).click();
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
    await page.goto("/library");
    await expect(heading(page, "需要管理员权限")).toBeVisible();
    // 成员的导航只有概览与专辑：没有扫描目录与成员这两个管理入口。
    await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link")).toHaveText(["概览", "专辑"]);
    expect(mock.callsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
    expect(mock.callsTo("GET", "/api/v1/library-root-operations")).toHaveLength(0);
  });

  test("接口返回 403：只拒绝当前操作，会话与导航保留", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.forbid.add("GET /api/v1/library-roots");
    await page.goto("/library");
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
