import { resolve } from "node:path";
import type { Page } from "@playwright/test";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { demoPlugin, PLUGINS_API } from "./support/plugins";

const { id, name, fileName, sourceId, token } = demoPlugin;
const screenshots = resolve(import.meta.dirname, "../../temp/m8-plugin-shots");
const emptyList = /^还没有安装插件。/;
const deletePath = `${PLUGINS_API}/${id}`;
const sourcePath = `/api/v1/admin/curation/sources/${encodeURIComponent(sourceId)}`;
const cardOn = (page: Page) => page.getByRole("region", { name: "插件", exact: true });

async function uploadForPreview(page: Page) {
  const card = cardOn(page);
  await expect(card.getByText(emptyList)).toBeVisible();
  const chooserPromise = page.waitForEvent("filechooser");
  await card.getByRole("button", { name: "上传插件" }).click();
  const chooser = await chooserPromise;
  // 空 ZIP 用来验证浏览器的 multipart 上传，受控后端返回预检样本。
  await chooser.setFiles({ name: fileName, mimeType: "application/zip", buffer: Buffer.from("504b0506000000000000000000000000000000000000", "hex") });
  const dialog = page.getByRole("dialog", { name: `安装插件“${name}”？` });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function openDelete(page: Page) {
  await cardOn(page).getByRole("button", { name: `${name} 的更多操作` }).click();
  await page.getByRole("menuitem", { name: `删除 ${name}` }).click();
  const dialog = page.getByRole("dialog", { name: `删除插件“${name}”？` });
  await expect(dialog).toBeVisible();
  return dialog;
}

for (const width of [390, 1440]) {
  test(`${width}px：上传、安装、打开外部源、删除插件，页面与确认框无横向溢出`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    await page.goto("/settings/curation");
    const card = cardOn(page);
    const sources = page.getByRole("region", { name: "外部源", exact: true });
    const toggle = sources.getByRole("switch", { name: `${name}（插件）` });
    await expect(sources.getByRole("switch", { name: "MusicBrainz" })).toBeVisible();
    await expect(toggle).toHaveCount(0);

    const preview = await uploadForPreview(page);
    await expect(preview.getByText("https://lyrics.example.org/api", { exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page, `安装确认 ${width}px`);
    const bounds = await preview.evaluate((element) => {
      const { left, right } = element.getBoundingClientRect();
      return { left, right, viewport: innerWidth, overflow: element.scrollWidth - element.clientWidth };
    });
    expect(bounds.left).toBeGreaterThanOrEqual(0);
    expect(bounds.right).toBeLessThanOrEqual(bounds.viewport);
    expect(bounds.overflow).toBeLessThanOrEqual(0);
    await preview.screenshot({ path: resolve(screenshots, `install-dialog-${width}.png`), animations: "disabled" });
    expect(mock.callsTo("POST", `${PLUGINS_API}/preview`)).toHaveLength(1);
    expect(mock.callsTo("POST", PLUGINS_API)).toHaveLength(0);

    await preview.getByRole("button", { name: "安装", exact: true }).click();
    await expect(preview).toBeHidden();
    await expect(card.getByText(name, { exact: true })).toBeVisible();
    await expect(toggle).toBeVisible();
    await expect(toggle).not.toBeChecked();
    expect(mock.callsTo("POST", PLUGINS_API).map(({ body }) => body)).toEqual([{ preview_token: token }]);
    await expectNoHorizontalOverflow(page, `插件已安装 ${width}px`);
    await card.screenshot({ path: resolve(screenshots, `plugins-card-${width}.png`), animations: "disabled" });

    await toggle.click();
    await expect(toggle).toBeChecked();
    await expect.poll(() => mock.callsTo("PUT", sourcePath).map(({ body }) => body)).toEqual([{ enabled: true }]);
    const deletion = await openDelete(page);
    await deletion.getByRole("button", { name: "删除", exact: true }).click();
    await expect(deletion).toBeHidden();
    await expect(card.getByText(emptyList)).toBeVisible();
    await expect(toggle).toHaveCount(0);
    expect(mock.callsTo("DELETE", deletePath)).toHaveLength(1);
    await expectNoHorizontalOverflow(page, `插件已删除 ${width}px`);
  });
}

test("删除失败后，提示应在仍打开的确认框内可读", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  await page.goto("/settings/curation");
  const preview = await uploadForPreview(page);
  await preview.getByRole("button", { name: "安装", exact: true }).click();
  await expect(preview).toBeHidden();

  mock.forbid.add(`DELETE ${deletePath}`);
  const dialog = await openDelete(page);
  await dialog.getByRole("button", { name: "删除", exact: true }).click();
  await expect(cardOn(page).getByText("插件没有删除", { exact: true })).toBeVisible();
  expect(mock.callsTo("DELETE", deletePath)).toHaveLength(1);
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("alert")).toBeVisible({ timeout: 2000 });
});
