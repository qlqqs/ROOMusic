import type { Page } from "@playwright/test";
import { releaseDetailOf } from "../src/test/catalog-data";
import { respondToGroupDefault } from "../src/test/governance-data";
import { LONG_ARTIST, LONG_TITLE, multiVersionGroup, seedVersions } from "./support/albums-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { expectFocusStaysInDialog, tabTo } from "./support/keyboard";
import { heading, uuidPattern } from "./support/locators";

// 默认版本与版本对比（M4）：管理员设定、改回与撤销默认版本，所有成员看到标签并打开对比对话框。
// 受控后端保持专辑的版本号、默认版本与记录的状态，所以页面能走完整的流程；宽度、axe 与键盘路径在真实浏览器里检查。

const album = () => multiVersionGroup();
const TITLE = "Symphony No. 9 in D minor, Op. 125 “Choral”";
const groupPath = () => `/albums/${album().id}`;
const defaultCalls = (mock: ApiMock) => mock.callsTo("POST", `/api/v1/release-groups/${album().id}/default`);

function start(mock: ApiMock) {
  mock.groups = [album()];
  return seedVersions(mock, album());
}

/** 预置：直接调用与页面共用的接口行为，把某个版本设为默认版本（专辑的版本号随之加一）。 */
function seedDefault(mock: ApiMock, releaseId: string | null, revision: number, keyIndex: number) {
  const result = respondToGroupDefault(mock.governance, album().id, `00000000-0000-4000-8000-${String(0xd000 + keyIndex).padStart(12, "0")}`, { expected_revision: revision, release_id: releaseId });
  if (result.status !== 200) throw new Error(`预置默认版本失败：${JSON.stringify(result.body)}`);
}

/** 头部概况里的曲目数：每个版本的曲目数不同，据此知道页面显示的是哪个版本。 */
const shownTracks = (page: Page) => page.locator("dt", { hasText: /^曲目$/ }).first().locator("xpath=following-sibling::dd[1]");
const versionNav = (page: Page) => page.getByRole("navigation", { name: "版本" });
const comparisonDialog = (page: Page) => page.getByRole("dialog", { name: "对比版本" });

test.describe("设定默认版本", () => {
  test("设为默认版本、改回自动选择与撤销：页面始终停在刚才的版本，版本导航与按钮随之变化", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const versions = start(mock);
    await page.goto(`${groupPath()}?release=${versions[1]?.id}`);
    await expect(heading(page, TITLE)).toBeVisible();
    await expect(shownTracks(page)).toHaveText("13 首");
    await expect(versionNav(page).getByText("默认版本")).toHaveCount(0);

    await page.getByRole("button", { name: "设为默认版本" }).click();
    await expect(page.getByText("已把这个版本设为默认版本。专辑列表和打开这张专辑时会先显示它。")).toBeVisible();
    const [set] = defaultCalls(mock);
    expect(set?.body).toEqual({ expected_revision: 1, release_id: versions[1]?.id });
    expect(set?.idempotencyKey).toMatch(uuidPattern);
    // 它成为代表版本：排在版本导航第一位并带标签，地址不再带 release，页面仍显示它。
    const first = versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ });
    await expect(first).toBeVisible();
    // 标签贴着文字，不被版本卡片（网格）拉满整行。
    const tagWidth = (await first.getByText("默认版本").boundingBox())?.width ?? 0;
    const cardWidth = (await first.boundingBox())?.width ?? 0;
    expect(tagWidth, "“默认版本”标签的宽度").toBeLessThan(cardWidth / 2);
    await expect(page).not.toHaveURL(/release=/);
    await expect(shownTracks(page)).toHaveText("13 首");
    await expect(page.getByRole("button", { name: "改回自动选择" })).toBeVisible();

    await page.getByRole("button", { name: "改回自动选择" }).click();
    await expect(page.getByText("已改回自动选择：这张专辑默认显示年份最早的版本。")).toBeVisible();
    expect(defaultCalls(mock)[1]?.body).toEqual({ expected_revision: 2, release_id: null });
    // 这个版本不再是代表版本，地址带上 release，页面还在它上面。
    await expect(page).toHaveURL(new RegExp(`release=${versions[1]?.id}`));
    await expect(shownTracks(page)).toHaveText("13 首");
    await expect(versionNav(page).getByText("默认版本")).toHaveCount(0);

    await page.getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销默认版本的调整。")).toBeVisible();
    await expect(versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ })).toBeVisible();
    await expect(shownTracks(page)).toHaveText("13 首");
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  test("只用键盘完成设定：Tab 到“设为默认版本”，Enter 提交，焦点留在按钮上，提示里的“撤销”也能用键盘到达", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(groupPath());
    await expect(heading(page, TITLE)).toBeVisible();
    await tabTo(page, (name) => name === "设为默认版本", 40);
    await expectVisibleFocusRing(page, "设为默认版本按钮");
    await page.keyboard.press("Enter");
    await expect(page.getByText("已把这个版本设为默认版本。专辑列表和打开这张专辑时会先显示它。")).toBeVisible();
    // 提交之后焦点没有丢到页面顶部：仍在这个按钮上（它现在叫“改回自动选择”）。
    await expect(page.getByRole("button", { name: "改回自动选择" })).toBeFocused();
    await tabTo(page, (name) => name === "撤销", 8);
    await expectVisibleFocusRing(page, "撤销按钮");
    await page.keyboard.press("Enter");
    await expect(page.getByText("已撤销默认版本的调整。")).toBeVisible();
  });

  test("普通成员没有设定入口，也不请求写接口；但看得到“默认版本”标签", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const versions = start(mock);
    seedDefault(mock, versions[2]?.id ?? null, 1, 1);
    await page.goto(groupPath());
    await expect(heading(page, TITLE)).toBeVisible();
    await expect(versionNav(page).getByRole("link", { name: /^版本 1 默认版本/ })).toBeVisible();
    await expect(page.getByRole("button", { name: "设为默认版本" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "改回自动选择" })).toHaveCount(0);
    expect(defaultCalls(mock)).toHaveLength(0);
  });
});

test.describe("版本对比", () => {
  test("所有成员都能打开：与第一列不同的格有下划线（浏览器里真的画出来），没有值写“—”，从列头切换版本后关闭对话框、焦点回到“对比版本”", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    const versions = start(mock);
    await page.goto(groupPath());
    await expect(heading(page, TITLE)).toBeVisible();
    await page.getByRole("button", { name: "对比版本" }).click();
    const dialog = comparisonDialog(page);
    const table = dialog.getByRole("table", { name: "各个版本的对比" });
    await expect(table).toBeVisible();
    await expect(table.getByRole("rowheader", { name: "年份" })).toBeVisible();

    const year = table.getByRole("row", { name: /^年份/ });
    await expect(year.getByRole("cell")).toHaveText(["1977", "1989（与版本 1 不同）", "2001（与版本 1 不同）"]);
    // 差异格的文字带金色下划线；基准列没有。
    const underline = (cell: ReturnType<typeof year.getByRole>) => cell.locator(".text, span").first().evaluate((element) => getComputedStyle(element).textDecorationLine);
    expect(await underline(year.getByRole("cell").nth(1))).toContain("underline");
    expect(await underline(year.getByRole("cell").nth(0))).not.toContain("underline");
    // 样本里的版本没有来源类型与介质类型：整行不出现。
    await expect(table.getByRole("rowheader", { name: "来源类型" })).toHaveCount(0);

    // 真实浏览器把绝对定位的“仅供读屏”文字当作块级元素，可访问名称里它与前面的文字之间有空格。
    await dialog.getByRole("link", { name: /^查看这个版本\s*（版本 2）$/ }).click();
    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`release=${versions[1]?.id}`));
    await expect(shownTracks(page)).toHaveText("13 首");
    await expect(page.getByRole("button", { name: "对比版本" })).toBeFocused();
  });

  test("只用键盘：Enter 打开，焦点先在标题上并被约束在对话框里，Esc 关闭后回到“对比版本”", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    start(mock);
    await page.goto(groupPath());
    await expect(heading(page, TITLE)).toBeVisible();
    await tabTo(page, (name) => name === "对比版本", 40);
    await expectVisibleFocusRing(page, "对比版本按钮");
    await page.keyboard.press("Enter");
    const dialog = comparisonDialog(page);
    await expect(dialog.getByRole("table", { name: "各个版本的对比" })).toBeVisible();
    await expect(dialog.getByRole("heading", { level: 2, name: "对比版本" })).toBeFocused();
    await expectFocusStaysInDialog(page, dialog, 10);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("button", { name: "对比版本" })).toBeFocused();
  });

  // 第二个版本的标题、版本说明与厂牌都很长：对比表里每一格都要在对话框内换行，不撑出横向滚动。
  const seedLongVersion = (mock: ApiMock, versions: ReturnType<typeof start>) => {
    const second = versions[1];
    if (second === undefined) throw new Error("没有第二个版本");
    mock.releases.set(second.id, releaseDetailOf({ ...second, title: LONG_TITLE.slice(0, 400), album_artist: LONG_ARTIST }, { edition: LONG_TITLE.slice(0, 300), label: LONG_ARTIST, catalog_number: "X".repeat(120) }));
    mock.groupDetails.set(album().id, { ...(mock.groupDetails.get(album().id) as NonNullable<ReturnType<typeof mock.groupDetails.get>>), releases: versions.map((version) => (version.id === second.id ? { ...version, title: LONG_TITLE.slice(0, 400) } : version)) });
  };

  test("五种宽度下对比对话框（长内容、含默认版本标签）在视野内、内部滚动、没有横向溢出，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const versions = start(mock);
    seedLongVersion(mock, versions);
    seedDefault(mock, versions[2]?.id ?? null, 1, 1);
    await page.goto(groupPath());
    await expect(heading(page, TITLE)).toBeVisible();
    await page.getByRole("button", { name: "对比版本" }).click();
    const dialog = comparisonDialog(page);
    await expect(dialog.getByRole("table", { name: "各个版本的对比" })).toBeVisible();
    await expect(dialog.getByText("默认版本").first()).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 600 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `版本对比 @${width}px`);
      await expectMinimumFontSize(page, `版本对比 @${width}px`);
      await expectNoSeriousViolations(page, `版本对比 @${width}px`);
      const box = await dialog.boundingBox();
      expect(box?.x ?? -1, `对比对话框左边界 @${width}px`).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0), `对比对话框右边界 @${width}px`).toBeLessThanOrEqual(width);
      expect(box?.width ?? 0, `对比对话框宽度 @${width}px`).toBeLessThanOrEqual(980);
      // 对话框最高 90vh；内容更高时在对话框内部滚动，标题行与“关闭”始终可见。
      expect(box?.height ?? 0, `对比对话框高度 @${width}px`).toBeLessThanOrEqual(600 * 0.9 + 1);
      expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth), `对比对话框内部横向溢出 @${width}px`).toBeLessThanOrEqual(0);
      await dialog.evaluate((element) => element.scrollTo({ top: element.scrollHeight }));
      await expect(dialog.getByRole("button", { name: "关闭" }), `滚到底之后“关闭”仍可见 @${width}px`).toBeInViewport();
      await dialog.evaluate((element) => element.scrollTo({ top: 0 }));
      // 窄于 640px 的容器堆叠成“版本 N：值”，其余是真正的表格。
      const display = await dialog.locator("tbody tr").first().evaluate((element) => getComputedStyle(element).display);
      expect(display, `对比表的布局 @${width}px`).toBe(width < 768 ? "block" : "table-row");
      if (width < 768) {
        // 堆叠时版本名在左、值在右（同一行），不是上下两行。
        const layout = await dialog.locator("tbody td").first().evaluate((element) => {
          const value = element.firstElementChild?.getBoundingClientRect();
          return { display: getComputedStyle(element).display, label: getComputedStyle(element, "::before").content, valueLeft: value?.left ?? 0, cellLeft: element.getBoundingClientRect().left };
        });
        expect(layout.display, `堆叠的格是网格 @${width}px`).toBe("grid");
        expect(layout.label, `堆叠的格带版本名 @${width}px`).toMatch(/版本 1/);
        expect(layout.valueLeft - layout.cellLeft, `值在版本名的右边 @${width}px`).toBeGreaterThan(40);
      }
    }
  });
});

test.describe("默认版本的记录", () => {
  test("五种宽度下操作记录页与“修改记录”里的默认版本记录（版本标题很长）没有横向溢出，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    const versions = start(mock);
    const second = versions[1];
    if (second === undefined) throw new Error("没有第二个版本");
    mock.releases.set(second.id, releaseDetailOf({ ...second, title: LONG_TITLE.slice(0, 300) }, { edition: LONG_TITLE.slice(0, 120) }));
    mock.groupDetails.set(album().id, { ...(mock.groupDetails.get(album().id) as NonNullable<ReturnType<typeof mock.groupDetails.get>>), releases: versions.map((version) => (version.id === second.id ? { ...version, title: LONG_TITLE.slice(0, 300) } : version)) });
    seedDefault(mock, second.id, 1, 1);

    await page.goto("/operations");
    const table = page.getByRole("table", { name: "操作记录" });
    await expect(table.getByText("设定默认版本", { exact: true })).toBeVisible();
    // 改动列很长时，操作者的短名字仍在一行里，不在词中间断开（如“curato / r”）。
    await page.setViewportSize({ width: 1440, height: 900 });
    const actor = table.getByRole("cell", { name: "curator", exact: true }).first();
    const lineHeight = await actor.evaluate((element) => parseFloat(getComputedStyle(element).lineHeight));
    expect((await actor.locator("span").boundingBox())?.height ?? 0, "操作者占一行").toBeLessThan(lineHeight * 1.5);
    // 默认版本（标题很长的版本 2）是专辑的代表版本，专辑列链接的文字就是它的标题。
    await expect(table.getByRole("link", { name: LONG_TITLE.slice(0, 20) }).first()).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `默认版本记录（操作记录页） @${width}px`);
      await expectMinimumFontSize(page, `默认版本记录（操作记录页） @${width}px`);
      await expectNoSeriousViolations(page, `默认版本记录（操作记录页） @${width}px`);
    }

    await page.goto(groupPath());
    await expect(heading(page, LONG_TITLE.slice(0, 300))).toBeVisible();
    await page.locator("summary", { hasText: "修改记录" }).click();
    await expect(page.getByRole("table", { name: "修改记录" }).getByText("设定默认版本", { exact: true })).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `默认版本记录（修改记录） @${width}px`);
      await expectNoSeriousViolations(page, `默认版本记录（修改记录） @${width}px`);
    }
  });
});
