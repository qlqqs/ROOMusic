import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import { tabBar } from "./support/settings";
import { installStewardMock, seedConversation } from "./support/steward";

// 管家对话的冒烟：真实浏览器里的面板、整页、手机布局与流式回答的渲染。管家接口由 support/steward.ts 受控响应，
// 运行流一次性返回整段 SSE；行为细节（停止、重试、出错、会话管理）由 Vitest 的页面级测试覆盖。

test.describe("管家对话", () => {
  test("桌面：侧栏打开面板，发消息，看到工具摘要与表格回答，展开到整页", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await installStewardMock(page);
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();

    await page.getByRole("navigation", { name: "主导航" }).getByRole("button", { name: "小蓝", exact: true }).click();
    const panel = page.getByRole("complementary", { name: "小蓝" });
    await expect(panel).toBeVisible();
    // 面板挤占内容，当前页面不变。
    await expect(heading(page, "概览")).toBeVisible();
    const input = panel.getByRole("textbox", { name: "问问小蓝" });
    await expect(input).toBeFocused();
    await input.fill("贝九有几版？");
    await input.press("Enter");

    await expect(panel.getByText("贝九有几版？", { exact: true }).first()).toBeVisible();
    await expect(panel.getByText("查了 1 处资料，用时 1 秒")).toBeVisible();
    const table = panel.getByRole("table");
    await expect(table.getByRole("cell", { name: "拜罗伊特" })).toBeVisible();
    await expect(table.getByRole("cell", { name: "1942" })).toBeVisible();
    await expectNoHorizontalOverflow(page, "面板打开时的概览");

    await panel.getByText("查了 1 处资料，用时 1 秒").click();
    await expect(panel.getByText("浏览专辑")).toBeVisible();
    await expect(panel.getByText("2 张")).toBeVisible();

    await panel.getByRole("button", { name: "展开到整页" }).click();
    await expect(page).toHaveURL(/\/steward\/[0-9a-f-]{36}$/);
    await expect(panel).toBeHidden();
    const conversation = page.getByRole("region", { name: "对话" });
    await expect(conversation.getByRole("table").getByRole("cell", { name: "柏林" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "对话" }).getByRole("link", { name: /贝九有几版/ })).toHaveAttribute("aria-current", "page");
    await expectNoHorizontalOverflow(page, "整页对话");
  });

  test("390px：从标签栏进入完成一次对话，再切换会话，没有横向滚动", async ({ page, backend }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await backend.start({ signedInAs: "listener" });
    const mock = await installStewardMock(page);
    seedConversation(mock, "上一次的对话", "上个月新加了哪些专辑？");
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();

    await tabBar(page).getByRole("link", { name: "小蓝", exact: true }).click();
    await expect(page).toHaveURL(/\/steward$/);
    const input = page.getByRole("textbox", { name: "问问小蓝" });
    await input.fill("贝九有几版？");
    await input.press("Enter");
    await expect(page).toHaveURL(/\/steward\/[0-9a-f-]{36}$/);
    await expect(page.getByText("查了 1 处资料，用时 1 秒")).toBeVisible();
    await expect(page.getByRole("table").getByRole("cell", { name: "拜罗伊特" })).toBeVisible();
    await expectNoHorizontalOverflow(page, "手机对话");

    await page.getByRole("button", { name: "全部对话" }).click();
    const list = page.getByRole("navigation", { name: "对话" });
    await expect(list.getByRole("link", { name: /贝九有几版/ })).toBeVisible();
    await expectNoHorizontalOverflow(page, "手机会话列表");
    await list.getByRole("link", { name: "上一次的对话" }).click();
    await expect(heading(page, "上一次的对话")).toBeVisible();
    await expect(page.getByText("上个月新加了哪些专辑？")).toBeVisible();
    await expectNoHorizontalOverflow(page, "切换后的手机对话");

    await page.getByRole("button", { name: "新对话" }).click();
    await expect(page).toHaveURL(/\/steward$/);
    await expect(page.getByRole("textbox", { name: "问问小蓝" })).toBeVisible();
  });

  test("旧地址 /settings/tokens 转到 /settings/agent", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await installStewardMock(page);
    await page.goto("/settings/tokens");
    await expect(page).toHaveURL(/\/settings\/agent$/);
    await expect(heading(page, "Agent")).toBeVisible();
    await expect(page.getByRole("heading", { level: 2, name: "访问令牌" })).toBeVisible();
  });
});
