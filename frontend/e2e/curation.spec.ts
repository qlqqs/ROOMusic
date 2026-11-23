import { enableCuration, seedBatch, seedSuppression } from "../src/test/curation-data";
import { seedAutoOperation } from "../src/test/governance-data";
import { expectMinimumFontSize, expectNoHorizontalOverflow, expectNoSeriousViolations } from "./support/checks";
import type { ApiMock } from "./support/api-mock";
import { expect, test } from "./support/fixtures";

const BATCH = "00000000-0000-4000-8000-0000000000b1";

/** 一批两条外部对应与一条标记；进度显示正在整理。 */
function seedCuration(mock: ApiMock) {
  enableCuration(mock.curation);
  mock.curation.state = "running";
  mock.curation.releases = { total: 12, processed: 5, matched: 4, uncertain: 1, no_match: 0, try_later: 0 };
  seedBatch(mock.curation, { id: BATCH, openedAt: "2026-01-05T00:00:00Z" });
  const [first, second] = mock.groups;
  for (const group of [first, second]) {
    if (group === undefined) throw new Error("缺少专辑种子");
    seedAutoOperation(mock.governance, {
      type: "external_link",
      releaseId: group.representative.id,
      provider: "musicbrainz",
      basis: "MusicBrainz 发行，依据条码；这是一段很长很长的依据文字，用来检查窄屏下的换行不会撑出横向滚动",
      batchId: BATCH,
      changes: {
        kind: "external_link",
        source: "musicbrainz",
        method: "barcode",
        confidence: "certain",
        external: { title: `${group.representative.title}（外部发行的名字也可能很长很长）`, artist: "Sample Artist", date: "2015-03-02", country: "JP" },
        links: [{ object_type: "release", object_id: group.representative.id, external_id: "11111111-1111-4111-8111-111111111111" }],
      },
    });
  }
  seedSuppression(mock.curation, {
    id: "00000000-0000-4000-8000-0000000000d1",
    target_type: "release",
    target_id: first?.representative.id ?? "",
    subject: "label",
    value: { mode: "set", value: "A Very Long Label Name That Should Wrap Anywhere Without Overflowing The Card" },
    operation_id: null,
    created_at: "2026-01-05T00:00:00Z",
  });
}

for (const width of [1440, 375, 390]) {
  test(`自动整理设置 ${width}：五组、确认框与撤销对话框的布局`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    seedCuration(mock);
    await page.goto("/settings/curation");
    await expect(page.getByRole("heading", { name: "自动整理", exact: true, level: 1 })).toBeVisible();
    for (const name of ["进度", "外部源", "批次", "不再提出"]) await expect(page.getByRole("region", { name, exact: true })).toBeVisible();
    await expect(page.getByText("正在整理", { exact: true })).toBeVisible();
    await expect(page.getByRole("table", { name: "自动整理的批次" }).getByRole("row")).toHaveCount(2);
    await expectNoHorizontalOverflow(page, `自动整理 ${width}`);
    await expectNoSeriousViolations(page, `自动整理 ${width}`);
    await expectMinimumFontSize(page, `自动整理 ${width}`);
    await page.screenshot({ path: `/tmp/roomusic-e1-curation-${width}.png`, fullPage: true });

    // AcoustID 的两把密钥：输入框与保存后的末四位都不撑出横向滚动。
    const sources = page.getByRole("region", { name: "外部源", exact: true });
    for (const [label, value] of [["应用密钥", "abcdEFGH1234"], ["用户密钥", "userKEY9876"]] as const) {
      const form = sources.getByRole("form", { name: `AcoustID 的${label}` });
      await form.getByRole("textbox").fill(value);
      await form.getByRole("button", { name: "保存" }).click();
    }
    await expect(sources.getByText("已保存，末四位 1234")).toBeVisible();
    await expect(sources.getByText("已保存，末四位 9876")).toBeVisible();
    await expectNoHorizontalOverflow(page, `应用密钥 ${width}`);
    await sources.screenshot({ path: `/tmp/roomusic-b3-sources-${width}.png` });

    // 关掉再打开：打开前的确认框在视野内、不溢出。
    await page.getByRole("switch", { name: "自动整理" }).click();
    await expect(page.getByRole("switch", { name: "暂停" })).toHaveCount(0);
    await page.getByRole("switch", { name: "自动整理" }).click();
    const consent = page.getByRole("dialog", { name: "打开自动整理？" });
    await expect(consent).toBeVisible();
    expect(await consent.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(0);
    await expectNoSeriousViolations(page, `打开确认 ${width}`);
    await consent.getByRole("button", { name: "打开" }).click();
    await expect(page.getByRole("switch", { name: "暂停" })).toBeVisible();

    // 撤销这一批：对话框不溢出，完成后写出权威总数，只发一段请求。
    await page.getByRole("button", { name: /开始的批次的操作/ }).click();
    await page.getByRole("menuitem", { name: "撤销这一批" }).click();
    const dialog = page.getByRole("dialog", { name: "撤销这一批？" });
    await expect(dialog).toBeVisible();
    expect(await dialog.evaluate((element) => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(0);
    await dialog.getByRole("button", { name: "撤销" }).click();
    await expect(page.getByText(/这一批共 2 条，已撤销 2 条/)).toBeVisible();
    expect(mock.callsTo("POST", "/api/v1/admin/curation/reverts")).toHaveLength(1);
    await expectNoHorizontalOverflow(page, `自动整理撤销后 ${width}`);
  });
}

test("操作记录按批次筛选：依据换行、不溢出（375）", async ({ page, backend }) => {
  await page.setViewportSize({ width: 375, height: 900 });
  const mock = await backend.start({ signedInAs: "curator" });
  seedCuration(mock);
  await page.goto(`/settings/operations?batch=${BATCH}`);
  await expect(page.getByText("只显示这一批自动整理的记录。")).toBeVisible();
  await expect(page.getByText(/^依据：MusicBrainz 发行/).first()).toBeVisible();
  await expectNoHorizontalOverflow(page, "按批次筛选的操作记录 375");
  await expectNoSeriousViolations(page, "按批次筛选的操作记录 375");
});

test("成员看不到自动整理栏，直接打开地址是需要管理员权限，不请求接口", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  await page.goto("/settings/curation");
  await expect(page.getByRole("heading", { name: "需要管理员权限", level: 1 })).toBeVisible();
  expect(mock.calls.filter((call) => call.path.startsWith("/api/v1/admin/curation"))).toHaveLength(0);
});
