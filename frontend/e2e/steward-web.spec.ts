import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";
import { installStewardMock, seedWebConversation, WEB_RESULTS } from "./support/steward";

// 管家联网的冒烟（C8）：设置里的“联网搜索”一组（来源、SearXNG 地址、保存与测试搜索、模型自带的可选条件），
// 对话里模型自带搜索与读取网页的过程条目、回答下面的来源列表。管家接口由 support/steward.ts 受控响应；
// 运行中的 search、citations 事件、密钥与各种失败原因由 Vitest 的页面级测试覆盖。不请求任何真实的搜索服务或网页。

test.describe("管家联网", () => {
  test("桌面：设置里选 SearXNG 保存后测试搜索；模型自带在检测确认支持之前不能选", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    await page.goto("/settings/agent");
    await expect(heading(page, "Agent")).toBeVisible();

    const group = page.getByRole("region", { name: "联网搜索" });
    await expect(group.getByRole("radio", { name: /^不联网搜索/ })).toBeChecked();
    await expect(group.getByRole("radio", { name: /^模型自带的搜索/ })).toBeDisabled();
    await expect(page.getByRole("region", { name: "检测结果" }).getByText("自带搜索")).toBeVisible();

    await group.getByRole("radio", { name: /^自己的 SearXNG/ }).check();
    await group.getByRole("textbox", { name: "SearXNG 地址" }).fill("https://search.example.org");
    await group.getByRole("button", { name: "保存" }).click();
    await expect(group.getByText("已保存")).toBeVisible();
    expect(mock.requests.find((request) => request.method === "PUT" && request.path.endsWith("/settings"))?.body).toEqual({ search_source: "searxng", searxng_url: "https://search.example.org" });

    await group.getByRole("button", { name: "测试搜索" }).click();
    await expect(group.getByText("这次搜到 3 条结果。")).toBeVisible();
    await expectNoHorizontalOverflow(page, "联网搜索设置");
  });

  test("桌面：对话里的模型自带搜索与读取网页条目，回答下面的来源只显示标题与域名", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    const mock = await installStewardMock(page);
    const conversation = seedWebConversation(mock);
    await page.goto(`/steward/${conversation.id}`);

    const thread = page.getByRole("region", { name: "对话" });
    const sources = thread.getByRole("list", { name: "来源" });
    await expect(sources.getByRole("link", { name: WEB_RESULTS[0]?.title ?? "" })).toHaveAttribute("rel", "noopener noreferrer");
    await expect(sources.getByRole("link", { name: "archive.example.net" })).toHaveAttribute("target", "_blank");
    await expect(sources).not.toContainText("utm_source");

    await thread.getByText(/^查了 2 处资料/).click();
    const search = thread.getByRole("listitem").filter({ hasText: "联网搜索（模型自带）" }).first();
    await expect(search.getByText("Furtwängler Beethoven 9 Bayreuth 1951")).toBeVisible();
    await expect(search.getByRole("list", { name: "联网搜索（模型自带）的结果" }).getByRole("link")).toHaveCount(2);
    const fetch = thread.getByRole("listitem").filter({ hasText: "读取网页" }).first();
    await expect(fetch.getByText("example.org", { exact: true })).toBeVisible();
    await expect(fetch.getByText("已读取，过长的部分截掉了")).toBeVisible();
    await expectNoHorizontalOverflow(page, "联网的对话");
  });

  test("390px：设置里的联网搜索一组与对话里的来源列表都没有横向溢出", async ({ page, backend }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await backend.start({ signedInAs: "listener" });
    const mock = await installStewardMock(page, { settings: { search_source: "tavily", search_key_saved: true, search_key_last_four: "abcd" } });
    const conversation = seedWebConversation(mock);

    await page.goto("/settings/agent");
    const group = page.getByRole("region", { name: "联网搜索" });
    await expect(group.getByRole("radio", { name: /^Tavily/ })).toBeChecked();
    await expect(group.getByText("已保存，末四位 abcd")).toBeVisible();
    await expectNoHorizontalOverflow(page, "手机上的联网搜索设置");

    await page.goto(`/steward/${conversation.id}`);
    const thread = page.getByRole("region", { name: "对话" });
    await expect(thread.getByRole("list", { name: "来源" }).getByRole("link")).toHaveCount(2);
    await thread.getByText(/^查了 2 处资料/).click();
    await expect(thread.getByRole("list", { name: "联网搜索（模型自带）的结果" })).toBeVisible();
    await expectNoHorizontalOverflow(page, "手机上的联网对话");
  });
});
