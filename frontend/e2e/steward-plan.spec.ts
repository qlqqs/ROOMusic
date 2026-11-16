import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { installStewardMock, PLAN_OPERATION_ID, seedPlanConversation } from "./support/steward";

// 管家修改方案与模式的冒烟（C6；C7 起模式有三种，Steward 的冒烟在 steward-review.spec.ts）：真实浏览器里的方案卡片（勾选、应用、结果、撤销）、模式菜单与 Shift+Tab。
// 管家接口由 support/steward.ts 受控响应，确认接口一次性返回整段 SSE；校验顺序、作废、过期与摘要不一致由 Vitest 的页面级测试覆盖。

test.describe("管家的修改方案", () => {
  test("桌面：刷新后看到未处理的方案，去掉一项后应用，结果列表里撤销已执行的项", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    const conversation = seedPlanConversation(mock);
    await page.goto(`/steward/${conversation.id}`);

    const card = page.getByRole("region", { name: "修改方案" });
    await expect(card).toBeVisible();
    await expect(card.getByText("2 项，其中 1 项不能撤销")).toBeVisible();
    await expect(card.getByText("贝多芬：第九交响曲“合唱”")).toBeVisible();
    await expect(card.getByText("依据（外部资料）：来自 MusicBrainz，本地没有可核对的证据")).toBeVisible();
    await expect(card.getByText("不可撤销")).toBeVisible();
    await expectNoHorizontalOverflow(page, "方案卡片");

    // 不可撤销的修改没有“不再问我”。
    await expect(card.getByRole("checkbox", { name: "这次对话里，修正专辑信息不再问我" })).toBeVisible();
    await expect(card.getByText("这次对话里，合并艺人不再问我")).toHaveCount(0);

    await card.getByRole("checkbox", { name: "合并艺人 威廉·富特文格勒" }).uncheck();
    await card.getByRole("button", { name: "应用 1 项修改" }).click();

    await expect(card.getByText("已执行")).toBeVisible();
    await expect(card.getByText("未选")).toBeVisible();
    await expect(page.getByText("标题和厂牌改好了，艺人没有动。")).toBeVisible();
    const decision = mock.requests.find((request) => request.path.endsWith("/decision"));
    expect(decision?.body).toEqual({ digest: "synthetic-digest", accepted_item_ids: ["00000000-0000-4000-8000-000000007301"], auto_approve_tools: [] });
    // 刷新后按会话详情的 plans 恢复成结果列表。
    await page.reload();
    await expect(card).toBeVisible();
    await expect(card.getByText("应用了 1 项")).toBeVisible();
    await expect(card.getByText("已执行")).toBeVisible();
    await expect(card.getByText("未选")).toBeVisible();

    await card.getByRole("button", { name: /^撤销/ }).click();
    await expect(card.getByText("已撤销")).toBeVisible();
    expect(mock.requests.some((request) => request.path === `/api/v1/operations/${PLAN_OPERATION_ID}/revert`)).toBe(true);
    await expectNoHorizontalOverflow(page, "方案的结果列表");
  });

  test("模式：菜单写明直接执行的含义；Shift+Tab 只在输入框里轮换，发第一条消息时带上", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    await page.goto("/steward");

    const input = page.getByRole("textbox", { name: "问问小蓝" });
    const mode = page.getByRole("button", { name: /^模式：/ });
    await expect(mode).toHaveAccessibleName("模式：Assistant");
    await mode.click();
    const menu = page.getByRole("menu", { name: "模式" });
    await expect(menu.getByRole("menuitem", { name: /Operator 不询问，直接执行；外部资料里的内容也可能促成修改/ })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();

    await input.focus();
    await page.keyboard.press("Shift+Tab");
    await expect(mode).toHaveAccessibleName("模式：Steward");
    await page.keyboard.press("Shift+Tab");
    await expect(mode).toHaveAccessibleName("模式：Operator");
    await expect(page.getByText("修改曲库时不询问，直接执行")).toBeVisible();
    await expect(input).toBeFocused();

    await input.fill("把标题都改成中文");
    await input.press("Enter");
    await expect(page).toHaveURL(/\/steward\/[0-9a-f-]{36}$/);
    expect(mock.requests.find((request) => request.method === "POST" && request.path === "/api/v1/me/steward/conversations")?.body).toEqual({ mode: "operator" });
  });
});
