import { albumGroup, albumGroups } from "../src/test/catalog-data";
import { seedVersions } from "./support/albums-data";
import { expectVisibleFocusRing } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { expectFocusStaysInDialog, focusedName, tabTo } from "./support/keyboard";
import { heading } from "./support/locators";
import { openSettings, userTrigger } from "./support/settings";

// 只用键盘完成主要路径：登录、跳到主要内容、导航、停用确认对话框（焦点进入、约束、Esc 关闭、焦点恢复）、窄屏菜单。

test.describe("键盘", () => {
  test("只用键盘登录：Tab 到各字段，Enter 提交，焦点环可见", async ({ page, backend }) => {
    await backend.start();
    await page.goto("/");
    await expect(heading(page, "登录")).toBeVisible();

    await page.keyboard.press("Tab");
    await expect(page.getByLabel("用户名")).toBeFocused();
    await expectVisibleFocusRing(page, "用户名输入框");
    await page.keyboard.type("curator");
    await page.keyboard.press("Tab");
    await expect(page.getByLabel("密码")).toBeFocused();
    await page.keyboard.type("curator-password");
    await page.keyboard.press("Enter");
    await expect(heading(page, "概览")).toBeVisible();
  });

  test("跳转链接是第一个可聚焦元素，激活后焦点进入主内容区；导航用链接，Enter 激活", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await expect(heading(page, "概览")).toBeVisible();

    await page.keyboard.press("Tab");
    await expect(page.getByRole("link", { name: "跳到主要内容" })).toBeFocused();
    await expectVisibleFocusRing(page, "跳转链接");
    await page.keyboard.press("Enter");
    await expect(page.locator("main#main")).toBeFocused();

    await openSettings(page);
    await page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link", { name: "成员" }).focus();
    await expectVisibleFocusRing(page, "导航链接");
    await page.keyboard.press("Enter");
    await expect(heading(page, "成员")).toBeVisible();
    // 导航后焦点回到主内容区，读屏用户得到页面变化的反馈。
    await expect(page.locator("main#main")).toBeFocused();
  });

  test("最近播放：卡片是整张的链接；清除确认对话框约束焦点，Esc 还回焦点，Enter 确认后焦点到区块标题", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "listener" });
    mock.seedRecentPlays(3);
    await page.goto("/");
    const panel = page.getByRole("region", { name: "最近播放" });
    const cards = panel.getByRole("listitem").getByRole("link");
    await expect(cards).toHaveCount(3);
    await cards.first().focus();
    await expectVisibleFocusRing(page, "最近播放的卡片");

    const trigger = panel.getByRole("button", { name: "清除播放历史" });
    await trigger.focus();
    await expectVisibleFocusRing(page, "清除播放历史按钮");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "清除播放历史？" });
    await expect(dialog).toBeVisible();
    // 初始焦点在“取消”上，避免回车直接确认。
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await expectFocusStaysInDialog(page, dialog, 4);

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    expect(mock.callsTo("DELETE", "/api/v1/me/plays")).toHaveLength(0);

    await page.keyboard.press("Enter");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("清除播放历史");
    await page.keyboard.press("Enter");
    await expect(dialog).toBeHidden();
    expect(mock.callsTo("DELETE", "/api/v1/me/plays")).toHaveLength(1);
    // 打开对话框的按钮已经不存在：焦点到区块标题，读屏用户同时听到“已清除”的状态提示。
    await expect(panel.getByRole("heading", { level: 2, name: "最近播放" })).toBeFocused();
    await expect(panel.getByText("已清除你的播放历史。")).toBeVisible();
    await expect(cards).toHaveCount(0);
  });

  test("停用确认对话框：焦点进入且被约束，Esc 关闭并把焦点还给触发按钮，Enter 确认", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/library");
    const trigger = page.getByRole("button", { name: "停用目录 classical-a" });
    await expect(trigger).toBeVisible();
    await trigger.focus();
    await page.keyboard.press("Enter");

    const dialog = page.getByRole("dialog", { name: "停用目录“classical-a”？" });
    await expect(dialog).toBeVisible();
    // 初始焦点在“取消”上，避免回车直接确认。
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await expectVisibleFocusRing(page, "对话框的取消按钮");

    await expectFocusStaysInDialog(page, dialog, 6);

    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();

    // 再次打开，用键盘走到确认按钮并确认。
    await page.keyboard.press("Enter");
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Tab");
    expect(await focusedName(page)).toBe("停用目录");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已停用目录“classical-a”/)).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("扫描记录：键盘翻页并进入详情，焦点环可见", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    mock.seedHistory(25);
    await page.goto("/settings/library");
    const nav = page.getByRole("navigation", { name: "扫描记录分页" });
    await expect(nav.getByText("第 1 / 2 页")).toBeVisible();

    await nav.getByRole("link", { name: "下一页" }).focus();
    await expectVisibleFocusRing(page, "下一页链接");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/settings\/library\?scan_page=2$/);
    await expect(nav.getByText("第 2 / 2 页")).toBeVisible();

    const detail = page.getByRole("table", { name: "扫描记录" }).getByRole("link", { name: /^查看详情/ }).first();
    await detail.focus();
    await expectVisibleFocusRing(page, "查看详情链接");
    await page.keyboard.press("Enter");
    await expect(heading(page, "扫描详情")).toBeVisible();
  });

  test("窄屏菜单：Enter 打开，焦点进入并被约束，Esc 关闭并把焦点还给“菜单”按钮；选择链接后菜单关闭", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto("/");
    const menu = page.getByRole("button", { name: "菜单" });
    await menu.focus();
    await page.keyboard.press("Enter");

    const dialog = page.getByRole("dialog", { name: "菜单" });
    await expect(dialog).toBeVisible();
    await expectFocusStaysInDialog(page, dialog, 6);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(menu).toBeFocused();

    await page.keyboard.press("Enter");
    await userTrigger(page).focus();
    await page.keyboard.press("Enter");
    const accountMenu = dialog.getByRole("menu", { name: "账号", exact: true });
    await expect(accountMenu.getByRole("menuitem", { name: "设置", exact: true })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(heading(page, "账号")).toBeVisible();
    await page.getByRole("navigation", { name: "设置", exact: true }).getByRole("link", { name: "扫描目录" }).focus();
    await expectVisibleFocusRing(page, "扫描目录分栏链接");
    await page.keyboard.press("Enter");
    await expect(heading(page, "扫描目录")).toBeVisible();
    await expect(dialog).toBeHidden();
  });

  test("成员页：键盘完成新建（Tab 顺序与 Enter 提交）与对话框确认", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/members");
    await page.getByLabel("用户名").focus();
    await page.keyboard.type("keyboard-user");
    await page.keyboard.press("Tab");
    await page.keyboard.type("correct horse battery");
    await page.keyboard.press("Tab");
    await page.keyboard.type("correct horse battery");
    await page.keyboard.press("Enter");
    await expect(page.getByText(/已创建成员“keyboard-user”/)).toBeVisible();

    const trigger = page.getByRole("button", { name: "撤销会话 keyboard-user" });
    await trigger.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "撤销成员“keyboard-user”的全部会话？" });
    await expect(dialog.getByRole("button", { name: "取消" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test("专辑主路径只用键盘完成：搜索 → 筛选 → 翻页 → 打开详情 → 切换版本 → 展开证据 → 返回列表，焦点始终可见", async ({ page, backend }) => {
    const mock = await backend.start({ signedInAs: "curator" });
    // 每个专辑都需要关注，共 120 个：筛选后仍有三页；有三个版本的“Album Choral”是第二页的第一个。
    const choral = albumGroup(200, {
      title: "Album Choral",
      album_artist: "Berliner Philharmoniker",
      artist: "Berliner Philharmoniker",
      year: 1977,
      medium_count: 2,
      track_count: 12,
      release_count: 3,
      attention_count: 1,
      artwork: true,
    });
    mock.groups = [
      ...albumGroups(48, () => ({ attention_count: 1 })),
      choral,
      ...Array.from({ length: 71 }, (_, offset) => albumGroup(101 + offset, { attention_count: 1 })),
    ];
    seedVersions(mock, choral);
    const cards = page.locator('a[href^="/albums/"]');
    const pager = page.getByRole("navigation", { name: "专辑分页" });

    await page.goto("/albums");
    await expect(cards).toHaveCount(48);

    // 搜索：回车提交，地址记录搜索词。
    const search = page.getByRole("textbox", { name: "搜索专辑" });
    await search.focus();
    await expectVisibleFocusRing(page, "搜索框");
    await page.keyboard.type("Album");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/albums\?q=Album$/);

    // 筛选：Tab 到复选框，空格切换，立即生效。
    await tabTo(page, (name) => name.includes("只看需要关注"));
    await expectVisibleFocusRing(page, "只看需要关注");
    await page.keyboard.press("Space");
    await expect(page).toHaveURL(/\/albums\?q=Album&attention=required$/);
    await expect(page.getByRole("region", { name: "“Album”中需要关注的专辑" })).toBeVisible();

    // 翻页：页码链接在 48 张卡片之后，直接聚焦它；Enter 之后焦点被带到结果标题，Tab 是第一张卡片。
    const next = pager.getByRole("link", { name: "下一页" });
    await next.focus();
    await expectVisibleFocusRing(page, "下一页链接");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/albums\?q=Album&attention=required&page=2$/);
    const resultsHeading = page.getByRole("heading", { level: 2, name: "“Album”中需要关注的专辑" });
    await expect(resultsHeading).toBeFocused();
    await expectVisibleFocusRing(page, "结果标题");
    await page.keyboard.press("Tab");
    await expect(cards.first()).toContainText("Album Choral");
    await expect(cards.first()).toBeFocused();
    await expectVisibleFocusRing(page, "第一张卡片");

    // 打开详情：导航后焦点回到主内容区。
    await page.keyboard.press("Enter");
    await expect(heading(page, "Album Choral")).toBeVisible();
    await expect(page.locator("main#main")).toBeFocused();

    // 切换版本：Tab 到“版本 2”，Enter；只改查询参数，焦点留在链接上，当前版本随之变化。
    // 头部新增收藏与添加到歌单，给它们保留 Tab 停靠点，不直接聚焦版本链接。
    await tabTo(page, (name) => name.startsWith("版本 2"), 20);
    await expectVisibleFocusRing(page, "版本 2 链接");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\?release=[0-9a-f-]{36}$/);
    const version2 = page.getByRole("navigation", { name: "版本" }).getByRole("link", { name: /^版本 2 / });
    await expect(version2).toHaveAttribute("aria-current", "page");
    await expect(version2).toBeFocused();
    await expect(page.locator("ol > li")).toHaveCount(13);

    // 展开证据：Tab 到“来源与证据”，Enter 展开；管理员再 Tab 到“查看完整证据”，Enter 加载。
    // 13 首曲目各有播放、心形与更多，头部另有心形和添加到歌单；仍逐个 Tab 验证到达证据入口。
    await tabTo(page, (name) => name.startsWith("来源与证据"), 60);
    await expectVisibleFocusRing(page, "来源与证据");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("table", { name: "证据摘要" })).toBeVisible();
    await tabTo(page, (name) => name === "查看完整证据");
    await expectVisibleFocusRing(page, "查看完整证据按钮");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("table", { name: "字段证据" })).toBeVisible();
    await expect(page.getByRole("button", { name: "重新加载完整证据" })).toBeFocused();

    // 返回列表：恢复搜索词、筛选与页码。
    const back = page.getByRole("link", { name: "返回专辑列表" });
    await back.focus();
    await expectVisibleFocusRing(page, "返回专辑列表链接");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/albums\?q=Album&attention=required&page=2$/);
    await expect(heading(page, "专辑")).toBeVisible();
    await expect(pager.getByText("第 2 / 3 页")).toBeVisible();
    await expect(cards.first()).toContainText("Album Choral");
  });
});
