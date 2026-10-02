import type { Page } from "@playwright/test";
import { albumGroups } from "../src/test/catalog-data";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 专辑列表的浏览器冒烟：搜索、筛选、翻页的地址与结果保持一致，封面的几种状态，空库说明。

/** 卡片就是指向专辑详情的链接。 */
const cards = (page: Page) => page.locator('a[href^="/albums/"]');
const searchBox = (page: Page) => page.getByRole("textbox", { name: "搜索专辑" });
const pager = (page: Page) => page.getByRole("navigation", { name: "专辑分页" });

/** 120 个专辑，每第 10 个需要关注：三页，筛选后 12 个。 */
const manyGroups = () => albumGroups(120, (index) => ({ attention_count: index % 10 === 0 ? 1 : 0 }));

test.describe("专辑列表", () => {
  test("搜索、只看需要关注、清除与翻页：地址、结果与分页一致；后退、前进与刷新还原；页码超出末页时替换为末页", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = manyGroups();
    await page.goto("/albums");
    await expect(heading(page, "专辑")).toBeVisible();
    await expect(cards(page)).toHaveCount(48);
    await expect(page.getByText("共 120 张专辑")).toBeVisible();
    await expect(pager(page).getByText("第 1 / 3 页")).toBeVisible();

    // 搜索：显式提交，地址记录搜索词。
    await searchBox(page).fill("Album 07");
    await searchBox(page).press("Enter");
    await expect(page).toHaveURL(/\/albums\?q=Album(\+|%20)07$/);
    await expect(cards(page)).toHaveCount(10);
    await expect(page.getByRole("region", { name: "“Album 07”的搜索结果" })).toBeVisible();
    await expect(page.getByText("找到 10 张专辑")).toBeVisible();

    // 只看需要关注：立即生效，与搜索组合。
    await page.getByRole("checkbox", { name: "只看需要关注" }).check();
    await expect(page).toHaveURL(/attention=required/);
    await expect(cards(page)).toHaveCount(1);
    await expect(page.getByRole("region", { name: "“Album 07”中需要关注的专辑" })).toBeVisible();

    // 清除搜索词，保留筛选；再取消筛选。
    await page.getByRole("button", { name: "清除" }).click();
    await expect(page).toHaveURL(/\/albums\?attention=required$/);
    await expect(cards(page)).toHaveCount(12);
    await expect(searchBox(page)).toBeFocused();
    await page.getByRole("checkbox", { name: "只看需要关注" }).uncheck();
    await expect(page).toHaveURL(/\/albums$/);
    await expect(cards(page)).toHaveCount(48);

    // 翻页写进地址；后退、前进与刷新都还原对应的页。
    await pager(page).getByRole("link", { name: "下一页" }).click();
    await expect(page).toHaveURL(/\/albums\?page=2$/);
    await expect(pager(page).getByText("第 2 / 3 页")).toBeVisible();
    await expect(cards(page).first()).toContainText("Album 049");
    await page.goBack();
    await expect(page).toHaveURL(/\/albums$/);
    await expect(pager(page).getByText("第 1 / 3 页")).toBeVisible();
    await page.goForward();
    await page.reload();
    await expect(pager(page).getByText("第 2 / 3 页")).toBeVisible();

    // 超出末页：替换为末页，后退不会回到越界的页码。
    await page.goto("/albums?page=99");
    await expect(page).toHaveURL(/\/albums\?page=3$/);
    await expect(cards(page)).toHaveCount(24);
  });

  test("点开卡片进入详情的地址", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    mock.groups = manyGroups();
    await page.goto("/albums?q=Album%2005");
    await expect(cards(page)).toHaveCount(10);
    const first = cards(page).first();
    const href = await first.getAttribute("href");
    await first.click();
    await expect(page).toHaveURL(new RegExp(`${href ?? "no-href"}$`));
  });

  test("封面：有封面的图片真的加载，没有封面是文字版面，加载失败说明原因", async ({ page, backend }) => {
    // 默认 12 个专辑：序号能被 3 整除的没有封面（4 个），其余 8 个有封面。
    const mock = await backend.start({ signedInAs: "curator" });
    mock.brokenArtworks.add("cover-001.png");
    await page.goto("/albums");
    await expect(cards(page)).toHaveCount(12);
    await expect(page.getByText("封面无法加载")).toHaveCount(1);
    await expect(cards(page).first()).toContainText("封面无法加载");
    await expect
      .poll(() => page.locator('a[href^="/albums/"] img').evaluateAll((images) => images.filter((image) => (image as HTMLImageElement).naturalWidth > 0).length))
      .toBe(7);
    // 没有封面：文字版面里排着标题，不发图片请求。
    await expect(cards(page).nth(2)).toContainText("Album 003");
    await expect(cards(page).nth(2).locator("img")).toHaveCount(0);
    expect(mock.callsTo("GET", /\/api\/v1\/artworks\//).map((call) => call.path).sort()).toEqual(
      ["001", "002", "004", "005", "007", "008", "010", "011"].map((label) => `/api/v1/artworks/cover-${label}.png`),
    );
  });

  test("封面懒加载：图片都带 loading=lazy，滚动到底之后全部加载完成", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = albumGroups(48, () => ({ artwork: true }));
    await page.goto("/albums");
    await expect(cards(page)).toHaveCount(48);
    await expect(page.locator('a[href^="/albums/"] img[loading="lazy"]')).toHaveCount(48);
    await page.locator("#main").evaluate((main) => main.scrollTo(0, main.scrollHeight));
    await expect
      .poll(() => page.locator('a[href^="/albums/"] img').evaluateAll((images) => images.filter((image) => (image as HTMLImageElement).naturalWidth > 0).length))
      .toBe(48);
  });

  test("资料库没有专辑：管理员被引导去扫描目录，成员只被告知等待", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [];
    await page.goto("/albums");
    await expect(page.getByText(/资料库里还没有专辑。添加音乐目录并完成一次扫描后/)).toBeVisible();
    await page.getByRole("link", { name: "前往扫描目录" }).click();
    await expect(heading(page, "扫描目录")).toBeVisible();

    const memberPage = await page.context().newPage();
    mock.session = "listener";
    await memberPage.goto("/albums");
    await expect(memberPage.getByText("资料库里还没有专辑。请等待管理员添加音乐目录并完成扫描。")).toBeVisible();
    await expect(memberPage.getByRole("link", { name: "前往扫描目录" })).toHaveCount(0);
  });

  test("没有符合条件的专辑：说明并可以一键清除", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/albums?q=%E4%B8%8D%E5%AD%98%E5%9C%A8");
    await expect(page.getByText(/没有符合条件的专辑。/)).toBeVisible();
    await page.getByRole("link", { name: "清除筛选" }).click();
    await expect(page).toHaveURL(/\/albums$/);
    await expect(cards(page)).toHaveCount(12);
  });
});
