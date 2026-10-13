import { albumGroup, groupDetailOf, releaseVersions } from "../src/test/catalog-data";
import { markUncertain, respondToCorrect } from "../src/test/governance-data";
import type { ApiMock } from "./support/api-mock";
import { expectMinimumFontSize, expectNoHorizontalOverflow, expectNoSeriousViolations, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { expectFocusStaysInDialog, focusedName, tabTo } from "./support/keyboard";
import { heading, uuidPattern } from "./support/locators";

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
    await expect(page.getByText("已保存修改：标题、年份。重新扫描不会覆盖它们。")).toBeVisible();
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
    await page.locator("summary").click();
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
    await expect(dialog.getByText("年份必须是 1 到 9999 的整数。")).toBeVisible();
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
    await expect(page.getByText("已保存修改：流派。重新扫描不会覆盖它们。")).toBeVisible();
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
    await expect(dialog.getByText("输入标题或艺人，找到要归入的专辑。")).toBeVisible();
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
