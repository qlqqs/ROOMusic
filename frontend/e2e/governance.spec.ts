import { albumGroup, groupDetailOf, releaseVersions } from "../src/test/catalog-data";
import { markUncertain, respondToCorrect } from "../src/test/governance-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { expectFocusStaysInDialog, focusedName, tabTo } from "./support/keyboard";
import { heading, mainNav, uuidPattern } from "./support/locators";

// 管理员对发行的受治理写操作：编辑发行信息、调整归组、撤销与操作记录。受控后端保持修正与记录的状态，
// 所以页面能走完整的流程：保存之后列表与详情显示新的有效值，撤销之后回到原来的样子。

const symphonies = () => albumGroup(1, { title: "Sample Symphonies", album_artist: "Sample Orchestra", artist: "Sample Orchestra", year: 1998, medium_count: 2, track_count: 3, artwork: true });
const releaseId = () => symphonies().representative.id;
const detailPath = () => `/albums/${symphonies().id}`;
const quartets = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990, artwork: true });
const concertos = () => albumGroup(4, { title: "Delta Concertos", artist: "Delta Ensemble", year: 1985, release_count: 2 });
const seedGrouping = (mock: ApiMock) => {
  mock.groups = [symphonies(), quartets(), albumGroup(3, { title: "Gamma Sonatas", artist: "Gamma Duo", year: 2005 }), concertos()];
  mock.groupDetails.set(concertos().id, groupDetailOf(concertos(), releaseVersions(concertos(), 2)));
};

// 预置在受控后端上的记录：直接调用与页面共用的接口行为，每条把厂牌改成不同的值（预期版本依次递增）。
const seedKey = (index: number) => `00000000-0000-4000-8000-${String(0xc000 + index).padStart(12, "0")}`;
function seedLabelCorrections(mock: ApiMock, count: number, label = (index: number) => `Label ${index + 1}`) {
  for (let index = 0; index < count; index += 1) {
    const result = respondToCorrect(mock.governance, releaseId(), seedKey(index), { expected_revision: index + 1, changes: { label: { mode: "set", value: label(index) } } });
    if (result.status !== 200) throw new Error(`预置修正失败：${JSON.stringify(result.body)}`);
  }
}

test.describe("编辑发行信息", () => {
  test("修改标题与年份并保存：请求带版本号与幂等键，页面显示新的有效值，提示里的撤销恢复原样", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();

    await page.getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    await expect(dialog.getByRole("group", { name: "条码" })).toBeVisible();
    await dialog.getByRole("group", { name: "标题" }).getByRole("radio", { name: "自定义" }).check();
    await dialog.getByRole("textbox", { name: "标题（自定义）" }).fill("Sample Symphonies (corrected)");
    await dialog.getByRole("group", { name: "年份" }).getByRole("radio", { name: "自定义" }).check();
    await dialog.getByRole("textbox", { name: "年份（自定义）" }).fill("1975");
    await expect(dialog.getByText("将修改：标题、年份。")).toBeVisible();
    await dialog.getByRole("button", { name: "保存修改" }).click();

    await expect(dialog).toBeHidden();
    await expect(heading(page, "Sample Symphonies (corrected)")).toBeVisible();
    await expect(page.getByText("已保存修改：标题、年份。")).toBeVisible();
    const [call] = mock.callsTo("PATCH", `/api/v1/releases/${releaseId()}/corrections`);
    expect(call?.body).toEqual({ expected_revision: 1, changes: { title: { mode: "set", value: "Sample Symphonies (corrected)" }, year: { mode: "set", value: 1975 } } });
    expect(call?.idempotencyKey).toMatch(uuidPattern);
    // 年份的修正显示在头部，带“人工修正”标记。
    await expect(page.getByText("年份", { exact: true }).locator("..").getByText("1975")).toBeVisible();
    await expect(page.getByText("人工修正").first()).toBeVisible();

    await page.getByRole("button", { name: "撤销" }).click();
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    await expect(page.getByText("人工修正")).toHaveCount(0);
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(1);
  });

  test("只用键盘完成修改与撤销：焦点进入对话框并被约束，Esc 取消后回到入口，保存后回到入口", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    markUncertain(mock.governance, releaseId(), ["year"]);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();

    await tabTo(page, (name) => name === "编辑发行信息", 30);
    await expectVisibleFocusRing(page, "编辑发行信息按钮");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    await expect(dialog.getByRole("group", { name: "条码" })).toBeVisible();
    await expectFocusStaysInDialog(page, dialog, 14);

    // Esc 取消：没有请求，焦点回到入口。
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("button", { name: "编辑发行信息" })).toBeFocused();
    expect(mock.callsTo("PATCH", /corrections$/)).toHaveLength(0);

    // 重新打开，用方向键选择“自定义”，输入，再 Tab 到保存。
    await page.keyboard.press("Enter");
    await expect(dialog.getByRole("group", { name: "条码" })).toBeVisible();
    // 对话框打开时焦点在标题上（读取状态里没有别的稳定的可聚焦元素，字段出现之后焦点也不丢）；Tab 进入第一个字段的单选按钮。
    expect(await focusedName(page)).toBe("编辑发行信息");
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("使用扫描值");
    await page.keyboard.press("ArrowRight");
    await expect(dialog.getByRole("group", { name: "标题" }).getByRole("radio", { name: "自定义" })).toBeChecked();
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("标题（自定义）");
    await page.keyboard.press("Control+A");
    await page.keyboard.type("键盘改的标题");
    await tabTo(page, (name) => name === "保存修改", 40);
    await expectVisibleFocusRing(page, "保存修改按钮");
    await page.keyboard.press("Enter");

    await expect(dialog).toBeHidden();
    await expect(heading(page, "键盘改的标题")).toBeVisible();
    await expect(page.getByRole("button", { name: "编辑发行信息" })).toBeFocused();
    await tabTo(page, (name) => name === "撤销", 5);
    await page.keyboard.press("Enter");
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
  });

  test("“确认扫描值”：不确定的字段点一下就存成修正", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    markUncertain(mock.governance, releaseId(), ["title"]);
    await page.goto(detailPath());
    await page.getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    const title = dialog.getByRole("group", { name: "标题" });
    await expect(title.getByText("扫描结果不确定")).toBeVisible();
    await title.getByRole("button", { name: "确认扫描值" }).click();
    await dialog.getByRole("button", { name: "保存修改" }).click();
    await expect(dialog).toBeHidden();
    const [call] = mock.callsTo("PATCH", `/api/v1/releases/${releaseId()}/corrections`);
    expect(call?.body).toEqual({ expected_revision: 1, changes: { title: { mode: "set", value: "Sample Symphonies" } } });
    // 之后详情的证据里，这个字段标出“人工修正”。
    await page.locator("summary", { hasText: "来源与证据" }).click();
    await expect(page.getByRole("table", { name: "证据摘要" }).getByText("人工修正")).toBeVisible();
  });

  test("409：别人刚改过这张专辑，对话框保留输入并显示最新状态，再保存成功", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    await page.goto(detailPath());
    await page.getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    const title = dialog.getByRole("group", { name: "标题" });
    await title.getByRole("radio", { name: "自定义" }).check();
    await dialog.getByRole("textbox", { name: "标题（自定义）" }).fill("我的标题");
    respondToCorrect(mock.governance, releaseId(), "00000000-0000-4000-8000-0000000000c1", { expected_revision: 1, changes: { genre: { mode: "set", value: "Jazz" } } });
    await dialog.getByRole("button", { name: "保存修改" }).click();
    await expect(dialog.getByText("这张专辑刚被修改过")).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "标题（自定义）" })).toHaveValue("我的标题");
    await expect(dialog.getByRole("textbox", { name: "流派（自定义）" })).toHaveValue("Jazz");
    await dialog.getByRole("button", { name: "保存修改" }).click();
    await expect(dialog).toBeHidden();
    await expect(heading(page, "我的标题")).toBeVisible();
  });

  test("普通成员没有编辑入口，也不会请求修正状态", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    mock.groups = [symphonies()];
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByText("Track 1-01")).toBeVisible();
    await expect(page.getByRole("button", { name: "编辑发行信息" })).toHaveCount(0);
    expect(mock.callsTo("GET", /corrections$/)).toHaveLength(0);
  });
});

test.describe("编辑发行信息：宽度与无障碍", () => {
  test("五种宽度下对话框与页面没有横向溢出、字号不小于 12px，对话框通过 axe（含字段错误）", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    markUncertain(mock.governance, releaseId(), ["title", "year"]);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await page.getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    await expect(dialog.getByRole("group", { name: "条码" })).toBeVisible();
    await dialog.getByRole("group", { name: "年份" }).getByRole("radio", { name: "自定义" }).check();
    await dialog.getByRole("textbox", { name: "年份（自定义）" }).fill("19x5");
    await expect(dialog.getByText("请填写有效的年份。")).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `编辑发行信息 @${width}px`);
      await expectMinimumFontSize(page, `编辑发行信息 @${width}px`);
      await expectNoSeriousViolations(page, `编辑发行信息 @${width}px`);
      // 对话框自己不横向溢出，保存按钮始终在视野里（底部的操作条固定）。
      const box = await dialog.boundingBox();
      expect(box?.x ?? -1, `对话框左边界 @${width}px`).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0), `对话框右边界 @${width}px`).toBeLessThanOrEqual(width);
      await expect(dialog.getByRole("button", { name: "保存修改" })).toBeInViewport();
    }
  });

  test("保存成功后的提示通过 axe，五种宽度下不溢出", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    await page.goto(detailPath());
    await page.getByRole("button", { name: "编辑发行信息" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑发行信息" });
    await dialog.getByRole("group", { name: "流派" }).getByRole("radio", { name: "留空" }).check();
    await dialog.getByRole("button", { name: "保存修改" }).click();
    await expect(page.getByText("已保存修改：流派。")).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await expectNoHorizontalOverflow(page, `修改后的提示 @${width}px`);
      await expectMinimumFontSize(page, `修改后的提示 @${width}px`);
      await expectNoSeriousViolations(page, `修改后的提示 @${width}px`);
    }
  });
});

test.describe("调整归组", () => {
  test("归入其他专辑：搜索、选中并预览、确认；页面跳到目标专辑并选中这个版本，提示里的撤销回到原来的专辑", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    seedGrouping(mock);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();

    await page.getByRole("button", { name: "归入其他专辑…" }).click();
    const dialog = page.getByRole("dialog", { name: "把这个版本归入其他专辑" });
    await dialog.getByRole("textbox", { name: "搜索专辑" }).fill("Quartet");
    await dialog.getByRole("textbox", { name: "搜索专辑" }).press("Enter");
    await dialog.getByRole("radio", { name: /Beta Quartets/ }).check();
    await expect(dialog.getByRole("status")).toHaveText("归入后，这个版本会和《Beta Quartets》的 1 个版本放在一起，同属一张专辑。");
    await dialog.getByRole("button", { name: "归入这张专辑" }).click();

    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`/albums/${quartets().id}\\?release=${releaseId()}`));
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "版本" })).toBeVisible();
    await expect(page.getByText("已把这个版本归入《Beta Quartets》。")).toBeVisible();
    const [call] = mock.callsTo("POST", `/api/v1/releases/${releaseId()}/group`);
    expect(call?.body).toEqual({ expected_revision: 1, action: "join", group_id: quartets().id });
    expect(call?.idempotencyKey).toMatch(uuidPattern);

    await page.getByRole("button", { name: "撤销" }).click();
    await expect(page).toHaveURL(new RegExp(`/albums/${symphonies().id}$`));
    await expect(page.getByText("已撤销归组，这个版本回到了原来的专辑。")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "版本" })).toHaveCount(0);
  });

  test("只用键盘完成归入：Tab 到入口，搜索、方向键选择、确认，再撤销", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    seedGrouping(mock);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();

    await tabTo(page, (name) => name === "归入其他专辑…", 30);
    await expectVisibleFocusRing(page, "归入其他专辑按钮");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "把这个版本归入其他专辑" });
    await expect(dialog).toBeVisible();
    expect(await focusedName(page)).toBe("把这个版本归入其他专辑");
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("搜索专辑");
    await page.keyboard.type("Gamma");
    await page.keyboard.press("Enter");
    await expect(dialog.getByRole("radio", { name: /Gamma Sonatas/ })).toBeVisible();
    await tabTo(page, (name) => name.includes("Gamma Sonatas"), 4);
    await page.keyboard.press("Space");
    await expect(dialog.getByRole("radio", { name: /Gamma Sonatas/ })).toBeChecked();
    await tabTo(page, (name) => name === "归入这张专辑", 4);
    await expectVisibleFocusRing(page, "归入这张专辑按钮");
    await page.keyboard.press("Enter");
    await expect(dialog).toBeHidden();
    await expect(page.getByText("已把这个版本归入《Gamma Sonatas》。")).toBeVisible();
    await tabTo(page, (name) => name === "撤销", 30);
    await page.keyboard.press("Enter");
    await expect(page.getByText("已撤销归组，这个版本回到了原来的专辑。")).toBeVisible();
  });

  test("Esc 关闭归入对话框：没有请求，焦点回到入口按钮", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    seedGrouping(mock);
    await page.goto(detailPath());
    await page.getByRole("button", { name: "归入其他专辑…" }).click();
    const dialog = page.getByRole("dialog", { name: "把这个版本归入其他专辑" });
    await expect(dialog).toBeVisible();
    await expectFocusStaysInDialog(page, dialog, 6);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("button", { name: "归入其他专辑…" })).toBeFocused();
    expect(mock.callsTo("POST", /\/group$/)).toHaveLength(0);
  });

  test("移出为独立专辑：确认对话框、移出、页面跳到新的专辑；撤销回到原来的专辑", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    seedGrouping(mock);
    await page.goto(`/albums/${concertos().id}`);
    await expect(heading(page, "Delta Concertos")).toBeVisible();
    await page.getByRole("button", { name: "移出为独立专辑" }).click();
    const dialog = page.getByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await dialog.getByRole("button", { name: "移出" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("已把这个版本移出为独立专辑。")).toBeVisible();
    await expect(page).not.toHaveURL(new RegExp(`/albums/${concertos().id}`));
    await expect(page.getByRole("navigation", { name: "版本" })).toHaveCount(0);
    expect(mock.callsTo("POST", /\/group$/)[0]?.body).toEqual({ expected_revision: 1, action: "detach" });

    await page.getByRole("button", { name: "撤销" }).click();
    await expect(page).toHaveURL(new RegExp(`/albums/${concertos().id}`));
    await expect(page.getByText("已撤销移出，这个版本回到了原来的专辑。")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "版本" })).toBeVisible();
  });

  test("五种宽度下归入对话框（搜索结果与预览）与移出确认对话框没有横向溢出，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    seedGrouping(mock);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await page.getByRole("button", { name: "归入其他专辑…" }).click();
    const dialog = page.getByRole("dialog", { name: "把这个版本归入其他专辑" });
    await dialog.getByRole("textbox", { name: "搜索专辑" }).fill("e");
    await dialog.getByRole("textbox", { name: "搜索专辑" }).press("Enter");
    await dialog.getByRole("radio", { name: /Beta Quartets/ }).check();
    await expect(dialog.getByRole("status")).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `归入其他专辑 @${width}px`);
      await expectMinimumFontSize(page, `归入其他专辑 @${width}px`);
      await expectNoSeriousViolations(page, `归入其他专辑 @${width}px`);
      const box = await dialog.boundingBox();
      expect(box?.x ?? -1, `归入对话框左边界 @${width}px`).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0), `归入对话框右边界 @${width}px`).toBeLessThanOrEqual(width);
      await expect(dialog.getByRole("button", { name: "归入这张专辑" })).toBeInViewport();
    }
    await dialog.getByRole("button", { name: "取消" }).click();

    await page.goto(`/albums/${concertos().id}`);
    await page.getByRole("button", { name: "移出为独立专辑" }).click();
    const confirm = page.getByRole("dialog", { name: "把这个版本移出为独立专辑？" });
    await expect(confirm).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await expectNoHorizontalOverflow(page, `移出为独立专辑 @${width}px`);
      await expectMinimumFontSize(page, `移出为独立专辑 @${width}px`);
      await expectNoSeriousViolations(page, `移出为独立专辑 @${width}px`);
    }
  });
});

test.describe("操作记录", () => {
  test("操作记录页：从“资料库”导航进入；撤销先确认，成功后提示，列表多一条记录并且不再有可撤销的", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 2);
    await page.goto("/");
    await mainNav(page).getByRole("link", { name: "操作记录" }).click();
    await expect(heading(page, "操作记录")).toBeVisible();
    await expect(page).toHaveURL(/\/operations$/);

    const table = page.getByRole("table", { name: "操作记录" });
    await expect(table.getByRole("row")).toHaveCount(3);
    await expect(table.getByRole("link", { name: "Sample Symphonies" }).first()).toHaveAttribute("href", `/albums/${symphonies().id}?release=${releaseId()}`);
    await expect(table.getByText("厂牌：Label 1 → Label 2")).toBeVisible();
    await expect(table.getByText("之后又有修改")).toBeVisible();
    await expect(table.getByRole("button", { name: /^撤销/ })).toHaveCount(1);

    await table.getByRole("button", { name: /^撤销/ }).click();
    const dialog = page.getByRole("dialog", { name: "撤销这次修改？" });
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await expect(dialog.getByText("厂牌：Label 2 → Label 1")).toBeVisible();
    await dialog.getByRole("button", { name: "撤销" }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    await expect(table.getByRole("row")).toHaveCount(4);
    await expect(table.getByText("撤销修改")).toBeVisible();
    await expect(table.getByRole("button", { name: /^撤销/ })).toHaveCount(0);
    const [call] = mock.callsTo("POST", /\/revert$/);
    expect(call?.idempotencyKey).toMatch(uuidPattern);
  });

  test("翻页与筛选：每页 20 条，地址带页码；从专辑详情的“全部记录”进入只看这个版本", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies(), quartets()];
    seedLabelCorrections(mock, 22);
    await page.goto("/operations");
    await expect(page.getByRole("table", { name: "操作记录" }).getByRole("row")).toHaveCount(21);
    await page.getByRole("link", { name: "下一页" }).click();
    await expect(page).toHaveURL(/\/operations\?page=2$/);
    await expect(page.getByRole("table", { name: "操作记录" }).getByRole("row")).toHaveCount(3);
    await expect(page.getByText("第 2 / 2 页")).toBeVisible();

    await page.goto(`/operations?release=${quartets().representative.id}`);
    await expect(page.getByText("这个版本还没有操作记录。")).toBeVisible();
    await page.goto(`/operations?release=${releaseId()}`);
    await expect(page.getByText("只显示《Sample Symphonies》的记录。")).toBeVisible();
    await page.getByRole("link", { name: "查看全部记录" }).click();
    await expect(page).toHaveURL(/\/operations$/);
  });

  test("只用键盘完成撤销：Tab 到“撤销”，Esc 取消后焦点回到按钮；再次打开并确认，焦点落在区块标题", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 1);
    await page.goto("/operations");
    await expect(heading(page, "操作记录")).toBeVisible();
    await expect(page.getByRole("table", { name: "操作记录" })).toBeVisible();

    await tabTo(page, (name) => name.startsWith("撤销（"), 40);
    await expectVisibleFocusRing(page, "撤销按钮");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "撤销这次修改？" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await expectFocusStaysInDialog(page, dialog, 4);

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("button", { name: /^撤销/ })).toBeFocused();
    expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(0);

    await page.keyboard.press("Enter");
    await expect(dialog).toBeVisible();
    await tabTo(page, (name) => name === "撤销", 4);
    await expectVisibleFocusRing(page, "撤销确认按钮");
    await page.keyboard.press("Enter");
    await expect(dialog).toBeHidden();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    // 触发撤销的按钮已经消失：焦点带到区块标题，键盘用户不会掉回页面顶部。
    await expect(page.getByRole("heading", { level: 2, name: "全部记录" })).toBeFocused();
  });

  test("专辑详情的“修改记录”：折叠时不请求，展开后读取；撤销的提示在记录区；“全部记录”进入筛选后的记录页", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 1);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByText("Track 1-01")).toBeVisible();
    expect(mock.callsTo("GET", "/api/v1/operations")).toHaveLength(0);

    await page.locator("summary", { hasText: "修改记录" }).click();
    const table = page.getByRole("table", { name: "修改记录" });
    await expect(table.getByRole("row")).toHaveCount(2);
    await expect(table.getByRole("columnheader", { name: "专辑" })).toHaveCount(0);
    await expect(page.getByText("共 1 条。")).toBeVisible();
    await table.getByRole("button", { name: /^撤销/ }).click();
    await page.getByRole("dialog", { name: "撤销这次修改？" }).getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    await expect(table.getByRole("row")).toHaveCount(3);
    await expect(page.locator("summary", { hasText: "修改记录" })).toBeFocused();

    await page.getByRole("link", { name: "全部记录" }).click();
    // “全部记录”带这个版本与它所在专辑的两个筛选（专辑级的默认版本记录也在其中）。
    await expect(page).toHaveURL(new RegExp(`/operations\\?release=${releaseId()}&group=${symphonies().id}$`));
    await expect(page.getByText("只显示这个版本和它所在的专辑的记录。")).toBeVisible();
  });

  test("普通成员：导航里没有“操作记录”，直接访问显示需要管理员权限，从不请求记录接口", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    mock.groups = [symphonies()];
    await page.goto("/operations");
    await expect(heading(page, "需要管理员权限")).toBeVisible();
    await expect(mainNav(page).getByRole("link", { name: "操作记录" })).toHaveCount(0);
    expect(mock.callsTo("GET", "/api/v1/operations")).toHaveLength(0);
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await expect(page.getByText("Track 1-01")).toBeVisible();
    await expect(page.locator("summary", { hasText: "修改记录" })).toHaveCount(0);
  });
});

test.describe("操作记录：宽度与无障碍", () => {
  // 一条标题很长的修改：长标题与连续的长字符串都要在表格与对话框里换行，不撑出横向滚动。
  const longTitle = `${"超长的专辑标题".repeat(8)}${"A".repeat(60)}`;

  test("五种宽度下操作记录页（含分页与成功提示）没有横向溢出、字号不小于 12px，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 23, (index) => (index === 22 ? longTitle : `Label ${index + 1}`));
    await page.goto("/operations");
    const table = page.getByRole("table", { name: "操作记录" });
    await expect(table.getByRole("row")).toHaveCount(21);
    await table.getByRole("button", { name: /^撤销/ }).click();
    await page.getByRole("dialog", { name: "撤销这次修改？" }).getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `操作记录页 @${width}px`);
      await expectMinimumFontSize(page, `操作记录页 @${width}px`);
      await expectNoSeriousViolations(page, `操作记录页 @${width}px`);
    }
  });

  test("五种宽度下撤销确认对话框（长改动内容）在视野内、没有溢出，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 1, () => longTitle);
    await page.goto("/operations");
    await page.getByRole("table", { name: "操作记录" }).getByRole("button", { name: /^撤销/ }).click();
    const dialog = page.getByRole("dialog", { name: "撤销这次修改？" });
    await expect(dialog).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 800 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `撤销对话框 @${width}px`);
      await expectMinimumFontSize(page, `撤销对话框 @${width}px`);
      await expectNoSeriousViolations(page, `撤销对话框 @${width}px`);
      const box = await dialog.boundingBox();
      expect(box?.x ?? -1, `撤销对话框左边界 @${width}px`).toBeGreaterThanOrEqual(0);
      expect((box?.x ?? 0) + (box?.width ?? 0), `撤销对话框右边界 @${width}px`).toBeLessThanOrEqual(width);
      // 长改动内容在对话框里换行：对话框自己没有横向滚动。
      expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth), `撤销对话框内部横向溢出 @${width}px`).toBeLessThanOrEqual(0);
      await expect(dialog.getByRole("button", { name: "撤销" })).toBeInViewport();
    }
  });

  test("五种宽度下专辑详情展开的“修改记录”（含撤销提示）没有横向溢出，通过 axe", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.groups = [symphonies()];
    seedLabelCorrections(mock, 7, (index) => (index === 6 ? longTitle : `Label ${index + 1}`));
    await page.goto(detailPath());
    await expect(heading(page, "Sample Symphonies")).toBeVisible();
    await page.locator("summary", { hasText: "修改记录" }).click();
    await expect(page.getByRole("table", { name: "修改记录" }).getByRole("row")).toHaveCount(6);
    await expect(page.getByText("只显示最近 5 条，共 7 条。")).toBeVisible();
    await page.getByRole("table", { name: "修改记录" }).getByRole("button", { name: /^撤销/ }).click();
    await page.getByRole("dialog", { name: "撤销这次修改？" }).getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText("已撤销这次修改，相关字段恢复到修改之前的状态。")).toBeVisible();
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(() => document.fonts.ready);
      await expectNoHorizontalOverflow(page, `修改记录 @${width}px`);
      await expectMinimumFontSize(page, `修改记录 @${width}px`);
      await expectNoSeriousViolations(page, `修改记录 @${width}px`);
    }
  });
});
