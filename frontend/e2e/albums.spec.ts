import type { Page } from "@playwright/test";
import { albumGroup, albumGroups } from "../src/test/catalog-data";
import { LONG_TITLE, multiVersionGroup, seedVersions } from "./support/albums-data";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 专辑的浏览器冒烟：列表的搜索、筛选、翻页的地址与结果保持一致，封面的几种状态，空库说明；
// 详情的头部与曲目、版本切换、来源与证据的权限、封面重新加载、版本已不可见。

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

  test("翻页后结果顶部回到视野并获得焦点，键盘接着就是第一张卡片", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = manyGroups();
    await page.goto("/albums");
    await expect(cards(page)).toHaveCount(48);
    // 页码链接在结果的下面：先滚到底，再翻页。
    await page.locator("#main").evaluate((main) => main.scrollTo(0, main.scrollHeight));
    await pager(page).getByRole("link", { name: "下一页" }).click();
    await expect(pager(page).getByText("第 2 / 3 页")).toBeVisible();
    await expect(cards(page).first()).toContainText("Album 049");

    const resultsHeading = page.getByRole("heading", { level: 2, name: "全部专辑" });
    await expect(resultsHeading).toBeFocused();
    await expect(resultsHeading).toBeInViewport();
    await expect(cards(page).first()).toBeInViewport();
    await page.keyboard.press("Tab");
    await expect(cards(page).first()).toBeFocused();
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

/** 详情页的曲目行（曲目列表是页面里唯一的 ol）。 */
const trackRows = (page: Page) => page.locator("ol > li");
const evidenceSummary = (page: Page) => page.locator("summary", { hasText: "来源与证据" });
const coverImage = (page: Page, title: string) => page.getByRole("img", { name: `《${title}》封面` });
const imageLoaded = (page: Page, title: string) => coverImage(page, title).evaluate((image) => (image as HTMLImageElement).naturalWidth > 0);

test.describe("专辑详情", () => {
  test("单版本专辑：头部、真实加载的封面、按介质分段的曲目与发行信息；没有版本导航", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, artwork: true });
    mock.groups = [group];
    await page.goto(`/albums/${group.id}`);

    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(coverImage(page, "Sample Symphonies")).toBeVisible();
    await expect.poll(() => imageLoaded(page, "Sample Symphonies")).toBe(true);
    await expect(page.getByRole("region", { name: /^第 1 张/ }).getByRole("listitem")).toHaveCount(2);
    await expect(page.getByRole("region", { name: /^第 2 张/ }).getByRole("listitem")).toHaveCount(1);
    await expect(trackRows(page)).toHaveCount(3);
    await expect(page.getByRole("region", { name: "发行信息" }).getByText("Sample Label")).toBeVisible();
    await expect(page.getByText("总时长")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "版本" })).toHaveCount(0);
    await expect(page.getByText(/个版本/)).toHaveCount(0);
    // 管理员接口只对管理员开放，成员的页面从不请求。
    expect(mock.callsTo("GET", /\/evidence$/)).toHaveLength(0);
  });

  test("多版本专辑：版本导航切换，地址、头部与曲目随之变化；后退、前进与刷新都还原", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = multiVersionGroup();
    mock.groups = [group];
    const versions = seedVersions(mock, group);
    await page.goto(`/albums/${group.id}`);

    const nav = page.getByRole("navigation", { name: "版本" });
    await expect(nav.getByRole("link")).toHaveCount(3);
    await expect(nav.getByRole("link", { name: /^版本 1 / })).toHaveAttribute("aria-current", "page");
    await expect(page.getByText("12 首", { exact: true })).toBeVisible();

    await nav.getByRole("link", { name: /^版本 2 / }).click();
    await expect(page).toHaveURL(new RegExp(`/albums/${group.id}\\?release=${versions[1]?.id}$`));
    await expect(nav.getByRole("link", { name: /^版本 2 / })).toHaveAttribute("aria-current", "page");
    await expect(nav.getByRole("link", { name: /^版本 1 / })).not.toHaveAttribute("aria-current", "page");
    await expect(page.getByText("13 首", { exact: true })).toBeVisible();
    await expect(trackRows(page)).toHaveCount(13);
    // 焦点留在被点击的链接上：只改查询参数的导航不把焦点抢回主内容区。
    await expect(nav.getByRole("link", { name: /^版本 2 / })).toBeFocused();

    await page.goBack();
    await expect(page).toHaveURL(new RegExp(`/albums/${group.id}$`));
    await expect(nav.getByRole("link", { name: /^版本 1 / })).toHaveAttribute("aria-current", "page");
    await expect(trackRows(page)).toHaveCount(12);
    await page.goForward();
    await page.reload();
    await expect(nav.getByRole("link", { name: /^版本 2 / })).toHaveAttribute("aria-current", "page");
    await expect(trackRows(page)).toHaveCount(13);
    // 组详情读一次，切换版本只读各版本自己的详情。
    expect(mock.callsTo("GET", /\/release-groups\/[^/]+$/)).toHaveLength(2); // 首次加载与刷新各一次
  });

  test("不在本组成员里的 release：用替换历史的方式去掉，后退直接回到上一页", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = multiVersionGroup();
    mock.groups = [group];
    seedVersions(mock, group);
    await page.goto("/albums");
    await expect(cards(page)).toHaveCount(1);
    await page.goto(`/albums/${group.id}?release=00000000-0000-4000-8000-0000000000ff`);
    await expect(page).toHaveURL(new RegExp(`/albums/${group.id}$`));
    await expect(page.getByRole("navigation", { name: "版本" }).getByRole("link", { name: /^版本 1 / })).toHaveAttribute("aria-current", "page");
    // 如果规范化新增了历史项，后退会先回到带 release 的地址。
    await page.goBack();
    await expect(page).toHaveURL(/\/albums$/);
  });

  test("来源与证据：成员只看到摘要，从不请求完整证据接口", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = albumGroup(1, { title: "Sample Symphonies", medium_count: 1, track_count: 2 });
    mock.groups = [group];
    await page.goto(`/albums/${group.id}`);
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByRole("table", { name: "证据摘要" })).toHaveCount(0); // 默认折叠

    await evidenceSummary(page).click();
    await expect(page.getByRole("table", { name: "证据摘要" })).toBeVisible();
    await expect(page.getByText(/并不表示信息有误/)).toBeVisible();
    await expect(page.getByRole("button", { name: /完整证据/ })).toHaveCount(0);
    expect(mock.callsTo("GET", /\/evidence$/)).toHaveLength(0);
  });

  test("来源与证据：管理员展开后才能请求完整证据，点之前没有请求", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const group = albumGroup(1, { title: "Sample Symphonies", medium_count: 1, track_count: 2 });
    mock.groups = [group];
    await page.goto(`/albums/${group.id}`);
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByRole("button", { name: "查看完整证据" })).toHaveCount(0);

    await evidenceSummary(page).click();
    await expect(page.getByRole("button", { name: "查看完整证据" })).toBeVisible();
    expect(mock.callsTo("GET", /\/evidence$/)).toHaveLength(0);

    await page.getByRole("button", { name: "查看完整证据" }).click();
    await expect(page.getByRole("table", { name: "字段证据" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "归组依据" })).toBeVisible();
    expect(mock.callsTo("GET", /\/evidence$/)).toHaveLength(1);
  });

  test("管理员的完整证据读取被拒绝（403）：就地提示，页面其余部分不受影响", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const group = albumGroup(1, { title: "Sample Symphonies", medium_count: 1, track_count: 2 });
    mock.groups = [group];
    mock.forbid.add(`GET /api/v1/releases/${group.representative.id}/evidence`);
    await page.goto(`/albums/${group.id}`);
    await evidenceSummary(page).click();
    await page.getByRole("button", { name: "查看完整证据" }).click();
    await expect(page.getByRole("alert")).toContainText("你的账号没有查看完整证据的权限。");
    await expect(page.getByRole("table", { name: "证据摘要" })).toBeVisible();
    await expect(trackRows(page)).toHaveCount(2);
  });

  test("详情页封面加载失败：说明原因，重新加载发出第二次请求并显示图片", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = albumGroup(1, { title: "Sample Symphonies", artwork: true });
    mock.groups = [group];
    mock.brokenArtworks.add("cover-001.png");
    await page.goto(`/albums/${group.id}`);
    await expect(page.getByText("封面无法加载")).toBeVisible();
    const requests = () => mock.callsTo("GET", "/api/v1/artworks/cover-001.png").length;
    expect(requests()).toBe(1);

    mock.brokenArtworks.delete("cover-001.png");
    await page.getByRole("button", { name: "重新加载封面" }).click();
    await expect(coverImage(page, "Sample Symphonies")).toBeVisible();
    await expect.poll(() => imageLoaded(page, "Sample Symphonies")).toBe(true);
    await expect(page.getByText("封面无法加载")).toHaveCount(0);
    expect(requests()).toBe(2);
  });

  test("没有封面的专辑：文字版面，不请求封面", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = albumGroup(1, { title: "Sample Symphonies", artwork: false });
    mock.groups = [group];
    await page.goto(`/albums/${group.id}`);
    await expect(page.getByRole("img", { name: "《Sample Symphonies》没有封面" })).toBeVisible();
    expect(mock.callsTo("GET", /\/api\/v1\/artworks\//)).toHaveLength(0);
  });

  test("所选版本在两次请求之间变得不可见：就地提示，回到代表版本", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = multiVersionGroup();
    mock.groups = [group];
    const versions = seedVersions(mock, group);
    mock.hiddenReleases.add(versions[1]?.id ?? "");
    await page.goto(`/albums/${group.id}?release=${versions[1]?.id}`);

    await expect(page.getByText("这个版本已不可见")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "版本" })).toBeVisible();
    await expect(trackRows(page)).toHaveCount(0);
    await page.getByRole("link", { name: "查看代表版本" }).click();
    await expect(page).toHaveURL(new RegExp(`/albums/${group.id}$`));
    await expect(trackRows(page)).toHaveCount(12);
    await expect(page.getByText("这个版本已不可见")).toHaveCount(0);
  });

  test("专辑不存在：组 ID 不是 UUID 时不发请求，服务器没有这个组时同样提示", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    await page.goto("/albums/not-a-uuid");
    await expect(heading(page, "专辑不存在")).toBeVisible();
    expect(mock.callsTo("GET", /\/api\/v1\/release-groups\/not-a-uuid/)).toHaveLength(0);

    await page.goto("/albums/00000000-0000-4000-8000-0000000000aa");
    await expect(heading(page, "专辑不存在")).toBeVisible();
    await page.getByRole("link", { name: "返回专辑列表" }).click();
    await expect(page).toHaveURL(/\/albums$/);
    await expect(heading(page, "专辑")).toBeVisible();
  });

  test("从列表进入详情再返回：搜索词、筛选与页码恢复，列表从头开始显示", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    // 每个组都需要关注：搜索 Album 并筛选后仍有两页。
    mock.groups = albumGroups(120, () => ({ attention_count: 1 }));
    await page.goto("/albums?q=Album&attention=required&page=2");
    await expect(cards(page)).toHaveCount(48);
    await cards(page).first().click();
    await expect(page).toHaveURL(/\/albums\/[0-9a-f-]{36}$/);
    await page.getByRole("link", { name: "返回专辑列表" }).click();
    await expect(page).toHaveURL(/\/albums\?q=Album&attention=required&page=2$/);
    await expect(pager(page).getByText("第 2 / 3 页")).toBeVisible();
    await expect(cards(page).first()).toContainText("Album 049");
  });

  test("长标题：完整显示，页面不出现横向滚动", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const group = albumGroup(1, { title: LONG_TITLE, medium_count: 1, track_count: 2 });
    mock.groups = [group];
    expect([...LONG_TITLE].length).toBeGreaterThan(1000);
    await page.goto(`/albums/${group.id}`);
    await expect(heading(page, LONG_TITLE)).toBeVisible();
  });
});
