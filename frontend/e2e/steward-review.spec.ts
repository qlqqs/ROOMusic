import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { installStewardMock, PLAN_OPERATION_ID, REVIEW_MODEL, seedReviewedConversation, seedUnavailableReviewConversation } from "./support/steward";

// 管家审查与 Steward 模式的冒烟（C7）：真实浏览器里的审查结果（状态、理由、撤销、转人工的确认）、审查不可用后的重新审查、
// 模式菜单与 Shift+Tab 的三种模式、模型菜单里的审查模型。管家接口由 support/steward.ts 受控响应，重新审查与确认一次性返回整段 SSE；
// 运行中的“审查中”、开流前的拒绝与“我自己确认”由 Vitest 的页面级测试覆盖。

test.describe("管家的审查", () => {
  test("桌面：审查结果按项列出状态与理由；已执行的可以撤销，转人工的项确认后执行", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    const conversation = seedReviewedConversation(mock);
    await page.goto(`/steward/${conversation.id}`);

    const card = page.getByRole("region", { name: "审查结果" });
    await expect(card).toBeVisible();
    await expect(card.getByText(`由 ${REVIEW_MODEL} 独立审查，不带这次对话的内容`)).toBeVisible();
    await expect(card.getByText(/独立性有限/)).toHaveCount(0);
    await expect(page.getByRole("navigation", { name: "对话" }).getByText("1 项等你确认")).toBeVisible();

    const done = card.getByRole("listitem").filter({ hasText: "Beethoven Symphony No.9 (Bayreuth 1951)" }).first();
    await expect(done.getByText("已执行")).toBeVisible();
    await expect(done.getByText("目标明确，有 CUE 文件佐证，可以单独撤销")).toBeVisible();
    const rejected = card.getByRole("listitem").filter({ hasText: "Karl Richter 1958" });
    await expect(rejected.getByText("没有执行")).toBeVisible();
    await expect(rejected.getByText("审查没有通过")).toBeVisible();
    await expect(rejected.getByText("曲库里有两位同名的人，证据不足以判断是哪一位")).toBeVisible();
    const human = card.getByRole("listitem").filter({ hasText: "等你确认" }).first();
    await expect(human.getByText("按规则交给你确认，不是审查模型的判断。")).toBeVisible();
    await expectNoHorizontalOverflow(page, "审查结果");

    const confirm = card.getByRole("region", { name: "等你确认的修改" });
    await expect(confirm.getByRole("checkbox", { name: "合并艺人 格伦·古尔德" })).toBeChecked();
    await expect(confirm.getByText(/不再问我/)).toHaveCount(0);

    await done.getByRole("button", { name: /^撤销/ }).click();
    await expect(done.getByText("已撤销")).toBeVisible();
    expect(mock.requests.some((request) => request.path === `/api/v1/operations/${PLAN_OPERATION_ID}/revert`)).toBe(true);

    await confirm.getByRole("button", { name: "应用 1 项修改" }).click();
    await expect(confirm).toHaveCount(0);
    await expect(card.getByRole("listitem").filter({ hasText: "格伦·古尔德" }).first().getByText("已执行")).toBeVisible();
    const decision = mock.requests.find((request) => request.path.endsWith("/decision"));
    expect(decision?.body).toEqual({ digest: "synthetic-review-digest", accepted_item_ids: ["00000000-0000-4000-8000-000000007402"], auto_approve_tools: [] });
  });

  test("390px：审查不可用时什么都没执行；重新审查后批准的项执行，不可撤销的转给用户", async ({ page, backend }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    const conversation = seedUnavailableReviewConversation(mock);
    await page.goto(`/steward/${conversation.id}`);

    const card = page.getByRole("region", { name: "审查结果" });
    await expect(card.getByText("审查不可用，这份方案什么都没有执行。")).toBeVisible();
    await expect(card.getByText("已执行")).toHaveCount(0);
    await expectNoHorizontalOverflow(page, "审查不可用");

    await card.getByRole("button", { name: "重新审查" }).click();
    await expect(card.getByRole("listitem").filter({ hasText: "Karl Richter 1958" }).getByText("已执行")).toBeVisible();
    await expect(card.getByRole("region", { name: "等你确认的修改" }).getByRole("checkbox", { name: "合并艺人 格伦·古尔德" })).toBeVisible();
    await expect(card.getByRole("button", { name: "重新审查" })).toHaveCount(0);
    expect(mock.requests.filter((request) => request.path.endsWith("/review")).map((request) => [request.method, request.body])).toEqual([["POST", undefined]]);
    await expectNoHorizontalOverflow(page, "重新审查之后");
  });

  test("模式：菜单有 Steward 与说明；Shift+Tab 在三种模式间轮换；没有另选审查模型时模型菜单与提示标明审查用对话模型", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page, { reviewModel: "" });
    await page.goto("/steward");

    const input = page.getByRole("textbox", { name: "问问小蓝" });
    const mode = page.getByRole("button", { name: /^模式：/ });
    await mode.click();
    await expect(page.getByRole("menu", { name: "模式" }).getByRole("menuitem", { name: /Steward 由另一次模型调用审查，通过的自动执行/ })).toBeVisible();
    await page.keyboard.press("Escape");

    await input.focus();
    await page.keyboard.press("Shift+Tab");
    await expect(mode).toHaveAccessibleName("模式：Steward");
    await expect(page.getByText("修改曲库前由另一次模型调用审查，拿不准的会回来问你；审查与对话是同一个模型，独立性有限")).toBeVisible();
    await page.getByRole("button", { name: /^对话模型：/ }).click();
    await expect(page.getByRole("menu", { name: "对话模型" }).getByRole("menuitem", { name: /synthetic-model 当前，也用作审查模型/ })).toBeVisible();
    await page.keyboard.press("Escape");

    await input.focus();
    await page.keyboard.press("Shift+Tab");
    await expect(mode).toHaveAccessibleName("模式：Operator");
    await page.keyboard.press("Shift+Tab");
    await page.keyboard.press("Shift+Tab");
    await expect(mode).toHaveAccessibleName("模式：Steward");
    await input.fill("把待关注的问题过一遍");
    await input.press("Enter");
    await expect(page).toHaveURL(/\/steward\/[0-9a-f-]{36}$/);
    expect(mock.requests.find((request) => request.method === "POST" && request.path === "/api/v1/me/steward/conversations")?.body).toEqual({ mode: "steward" });
  });
});
