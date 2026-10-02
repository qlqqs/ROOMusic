import type { Page } from "@playwright/test";
import { sampleBody } from "../src/test/api-samples";
import { albumGroups } from "../src/test/catalog-data";
import type { DiagnosticsPageDTO } from "../src/features/library/api/decoders";
import type { ApiMock, MockOptions } from "./support/api-mock";
import { expectMinimumFontSize, expectNoSeriousViolations } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { heading } from "./support/locators";

// 无障碍扫描：每个页面状态用 axe（WCAG 2.1 A 与 AA）扫描，serious 或 critical 即失败，并检查最小字号。
// 除了默认状态，还覆盖对话框打开、错误提示、字段错误与窄屏菜单，这些状态的结构与默认状态不同。

type State = {
  name: string;
  options: MockOptions;
  /** 到达要扫描的状态；返回前页面应已稳定。 */
  reach: (page: Page, mock: ApiMock) => Promise<void>;
};

const failedScan = (mock: ApiMock) => {
  const diagnostics = sampleBody("scans/diagnostics-multiple-kinds") as DiagnosticsPageDTO;
  const scan = mock.seedScan({ status: "failed", finished_at: "2026-01-02T00:01:00Z", diagnostics: diagnostics.summary });
  mock.diagnostics.set(scan.id, diagnostics);
  return scan;
};

const states: State[] = [
  {
    name: "登录",
    options: {},
    reach: async (page) => {
      await page.goto("/");
      await expect(heading(page, "登录")).toBeVisible();
    },
  },
  {
    name: "登录（错误提示）",
    options: {},
    reach: async (page) => {
      await page.goto("/");
      await page.getByLabel("用户名").fill("curator");
      await page.getByLabel("密码").fill("wrong password");
      await page.getByRole("button", { name: "登录" }).click();
      await expect(page.getByRole("alert")).toBeVisible();
    },
  },
  {
    name: "初始化（字段错误）",
    options: { setupRequired: true },
    reach: async (page) => {
      await page.goto("/");
      await page.getByRole("button", { name: "创建管理员并登录" }).click();
      await expect(page.getByText("请输入用户名。")).toBeVisible();
    },
  },
  {
    name: "概览（管理员）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.seedScan({}, true);
      await page.goto("/");
      await expect(page.getByRole("link", { name: "查看扫描详情" })).toBeVisible();
    },
  },
  {
    name: "概览（成员）",
    options: { signedInAs: "listener" },
    reach: async (page) => {
      await page.goto("/");
      await expect(heading(page, "概览")).toBeVisible();
      await expect(page.getByText("没有正在运行的扫描。")).toBeVisible();
    },
  },
  {
    name: "专辑列表（有封面、需要关注与多版本）",
    options: { signedInAs: "listener" },
    reach: async (page, mock) => {
      mock.brokenArtworks.add("cover-002.png");
      await page.goto("/albums");
      await expect(page.getByText("共 12 张专辑")).toBeVisible();
      await expect(page.getByText("封面无法加载")).toBeVisible();
    },
  },
  {
    name: "专辑列表（多页，第二页）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.groups = albumGroups(100, (index) => ({ artwork: index % 2 === 0, attention_count: index % 7 === 0 ? 1 : 0 }));
      await page.goto("/albums?page=2");
      await expect(page.getByRole("navigation", { name: "专辑分页" }).getByText("第 2 / 3 页")).toBeVisible();
    },
  },
  {
    name: "专辑列表（搜索结果）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/albums?q=Album%200&attention=required");
      await expect(page.getByRole("region", { name: "“Album 0”中需要关注的专辑" })).toBeVisible();
    },
  },
  {
    name: "专辑列表（没有符合条件的专辑）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/albums?q=nothing");
      await expect(page.getByText(/没有符合条件的专辑。/)).toBeVisible();
    },
  },
  {
    name: "专辑列表（搜索词过长的字段错误）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/albums");
      await page.getByRole("textbox", { name: "搜索专辑" }).fill("长".repeat(70));
      await page.getByRole("button", { name: "搜索" }).click();
      await expect(page.getByText(/搜索词最多 200 个字节/)).toBeVisible();
    },
  },
  {
    name: "专辑列表（空库，管理员）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.groups = [];
      await page.goto("/albums");
      await expect(page.getByText(/资料库里还没有专辑。添加音乐目录/)).toBeVisible();
    },
  },
  {
    name: "专辑列表（空库，成员）",
    options: { signedInAs: "listener" },
    reach: async (page, mock) => {
      mock.groups = [];
      await page.goto("/albums");
      await expect(page.getByText(/请等待管理员添加音乐目录/)).toBeVisible();
    },
  },
  {
    name: "扫描目录",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/library");
      await expect(page.getByRole("rowheader", { name: "classical-a" })).toBeVisible();
    },
  },
  {
    name: "扫描目录（扫描记录，第一页）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.seedHistory(25);
      await page.goto("/library");
      await expect(page.getByRole("table", { name: "扫描记录" })).toBeVisible();
      await expect(page.getByRole("navigation", { name: "扫描记录分页" }).getByText("第 1 / 2 页")).toBeVisible();
    },
  },
  {
    name: "扫描目录（扫描记录，末页）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.seedHistory(25);
      await page.goto("/library?scan_page=2");
      await expect(page.getByRole("navigation", { name: "扫描记录分页" }).getByText("第 2 / 2 页")).toBeVisible();
    },
  },
  {
    name: "扫描目录（停用确认对话框）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/library");
      await page.getByRole("button", { name: "停用目录 classical-a" }).click();
      await expect(page.getByRole("dialog", { name: "停用目录“classical-a”？" })).toBeVisible();
    },
  },
  {
    name: "扫描目录（字段错误与结果提示）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/library");
      await page.getByLabel("目录路径").fill("/music/accessible");
      await page.getByRole("button", { name: "添加目录" }).click();
      await expect(page.getByText(/已添加目录“accessible”/)).toBeVisible();
      await page.getByLabel("目录路径").fill("/etc");
      await page.getByRole("button", { name: "添加目录" }).click();
      await expect(page.getByLabel("目录路径")).toHaveAttribute("aria-invalid", "true");
    },
  },
  {
    name: "扫描详情（运行中）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      mock.scanFinishesAfterReads = null;
      const scan = mock.seedScan({}, true);
      await page.goto(`/library/scans/${scan.id}`);
      await expect(page.getByRole("button", { name: "取消扫描" })).toBeVisible();
    },
  },
  {
    name: "扫描详情（失败，多类诊断）",
    options: { signedInAs: "curator" },
    reach: async (page, mock) => {
      const scan = failedScan(mock);
      await page.goto(`/library/scans/${scan.id}`);
      await expect(page.getByRole("table", { name: "诊断明细" })).toBeVisible();
    },
  },
  {
    name: "成员",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/members");
      await expect(page.getByRole("rowheader", { name: /^listener/ })).toBeVisible();
    },
  },
  {
    name: "成员（对自己的确认对话框）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/members");
      await page.getByRole("button", { name: "停用成员 curator" }).click();
      await expect(page.getByRole("dialog", { name: "停用成员“curator”？" })).toBeVisible();
    },
  },
  {
    name: "成员（字段错误与重复用户名）",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/members");
      await page.getByLabel("用户名").fill("listener");
      await page.getByLabel("密码", { exact: true }).fill("correct horse battery");
      await page.getByLabel("确认密码").fill("correct horse battery");
      await page.getByRole("button", { name: "创建成员" }).click();
      await expect(page.getByText("用户名已存在，请换一个。")).toBeVisible();
    },
  },
  {
    name: "页面不存在",
    options: { signedInAs: "curator" },
    reach: async (page) => {
      await page.goto("/nowhere");
      await expect(heading(page, "页面不存在")).toBeVisible();
    },
  },
  {
    name: "需要管理员权限",
    options: { signedInAs: "listener" },
    reach: async (page) => {
      await page.goto("/members");
      await expect(heading(page, "需要管理员权限")).toBeVisible();
    },
  },
  {
    name: "服务不可用",
    options: { failSetupStatusTimes: 99 },
    reach: async (page) => {
      await page.goto("/");
      await expect(heading(page, "暂时无法使用")).toBeVisible();
    },
  },
];

test.describe("axe 扫描（桌面 1280px）", () => {
  test.use({ viewport: { width: 1280, height: 900 } });
  for (const state of states) {
    test(state.name, async ({ page, backend }) => {
      const mock = await backend.start(state.options);
      await state.reach(page, mock);
      await page.evaluate(() => document.fonts.ready);
      await expectNoSeriousViolations(page, state.name);
      await expectMinimumFontSize(page, state.name);
    });
  }
});

test.describe("axe 扫描（手机 375px）", () => {
  test.use({ viewport: { width: 375, height: 800 } });
  for (const state of states) {
    test(state.name, async ({ page, backend }) => {
      const mock = await backend.start(state.options);
      await state.reach(page, mock);
      await page.evaluate(() => document.fonts.ready);
      await expectNoSeriousViolations(page, `${state.name}（375px）`);
      await expectMinimumFontSize(page, `${state.name}（375px）`);
    });
  }

  test("窄屏菜单（模态导航）", async ({ page, backend }) => {
    await backend.start({ signedInAs: "curator" });
    await page.goto("/");
    await page.getByRole("button", { name: "菜单" }).click();
    await expect(page.getByRole("dialog", { name: "菜单" })).toBeVisible();
    await expectNoSeriousViolations(page, "窄屏菜单");
    await expectMinimumFontSize(page, "窄屏菜单");
  });
});
