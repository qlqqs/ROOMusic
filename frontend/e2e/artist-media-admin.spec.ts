import { resolve } from "node:path";
import type { Page, Request } from "@playwright/test";
import { decodeArtistLinkPreview, decodeArtistMediaReject, decodeArtistMediaWrite } from "../src/features/artists/api/decoders";
import { sampleBody } from "../src/test/api-samples";
import type { ApiMock } from "./support/api-mock";
import { seedArtistMedia } from "./support/artist-media-seed";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";
import { gradientPng } from "./support/png";

function seed(mock: ApiMock) {
  const artist = seedArtistMedia(mock, "with-media");
  const detail = mock.artists?.details.get(artist.id);
  if (detail === undefined) throw new Error("缺少艺人详情样本");
  return { detail, url: `/artists/${artist.id}`, api: `/api/v1/artists/${artist.id}` };
}

function pasteLink(page: Page, url: string) {
  return page.locator("body").evaluate((body, text) => {
    const clipboardData = new DataTransfer();
    clipboardData.setData("text/plain", text);
    body.dispatchEvent(new ClipboardEvent("paste", { clipboardData, bubbles: true, cancelable: true }));
  }, url);
}

function expectWrite(request: Request, method: "POST" | "PUT", body: unknown) {
  expect(request.method()).toBe(method);
  expect(request.headers()["idempotency-key"]).toMatch(/^[0-9a-f-]{36}$/);
  expect(request.postDataJSON()).toEqual(body);
}

test("管理员从来源弹层查看资料，粘贴链接预览后确认指定对应", async ({ page, backend }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { detail, url, api } = seed(await backend.start({ signedInAs: "curator" }));
  const revision = detail.revision;
  const preview = { ...decodeArtistLinkPreview(sampleBody("artists/link-preview")), name_matches: true };
  const externalUrl = `https://musicbrainz.org/artist/${preview.external_id}`;
  const write = decodeArtistMediaWrite(sampleBody("artists/set-links"));
  await page.route(`**${api}/link-preview`, (route) => route.fulfill({ json: preview }));
  await page.route(`**${api}/links`, (route) => {
    detail.links = { musicbrainz: preview.musicbrainz, wikidata: preview.wikidata, judged_by_model: false, operation_id: null };
    detail.revision = revision + 1;
    return route.fulfill({ json: { ...write, artist_id: detail.artist.id, revision: detail.revision } });
  });

  await page.goto(url);
  const seal = page.getByRole("button", { name: /^图片来源：Wikimedia Commons/ });
  await seal.click();
  const source = page.getByRole("dialog", { name: "图片来源" });
  await expect(source.getByRole("link", { name: "Wikimedia Commons" })).toBeVisible();
  await expect(source.getByRole("link", { name: "CC BY 4.0" })).toBeVisible();
  await expect(source.getByText("合成样本作者")).toBeVisible();
  await expect(source.getByRole("button", { name: "图片不对" })).toBeVisible();
  await expect(source.getByRole("button", { name: "指定对应" })).toBeVisible();
  await expect(source.getByRole("button", { name: "更换图片" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(seal).toBeFocused();

  const previewRequest = page.waitForRequest(`**${api}/link-preview`);
  await pasteLink(page, externalUrl);
  const dialog = page.getByRole("dialog", { name: "指定对应" });
  const input = dialog.getByRole("textbox");
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(externalUrl);
  const request = await previewRequest;
  expect(request.method()).toBe("POST");
  expect(request.postDataJSON()).toEqual({ url: externalUrl });
  expect(request.headers()["idempotency-key"]).toBeUndefined();
  await expect(dialog.getByText(preview.name)).toBeVisible();
  await expect(dialog.getByLabel("名字一致", { exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page, "1440px 指定对应");
  await page.screenshot({ path: resolve(import.meta.dirname, "../../temp/artist-media-shots/admin-1440-link.png"), animations: "disabled" });

  const linksRequest = page.waitForRequest(`**${api}/links`);
  await dialog.getByRole("button", { name: "对应到这位" }).click();
  expectWrite(await linksRequest, "PUT", { expected_revision: revision, source: preview.source, external_id: preview.external_id });
  await expect(dialog).toHaveCount(0);
  await seal.click();
  await expect(source.getByRole("link", { name: "MusicBrainz" })).toHaveAttribute("href", externalUrl);
  await expect(source.getByRole("link", { name: `Wikidata ${preview.wikidata}` })).toBeVisible();
});

test("图片标记可以撤回，6 秒后才发送标记请求", async ({ page, backend }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await page.clock.install();
  const { detail, url, api } = seed(await backend.start({ signedInAs: "curator" }));
  const revision = detail.revision;
  const result = decodeArtistMediaReject(sampleBody("artists/reject-media"));
  const requests: Request[] = [];
  await page.route(`**${api}/media/reject`, (route) => {
    requests.push(route.request());
    detail.image = null;
    detail.artist.image = null;
    detail.revision = revision + 1;
    return route.fulfill({ json: { ...result, artist_id: detail.artist.id, revision: detail.revision, layer: "auto" } });
  });

  await page.goto(url);
  const seal = page.getByRole("button", { name: /^图片来源：Wikimedia Commons/ });
  const markWrong = page.getByRole("dialog", { name: "图片来源" }).getByRole("button", { name: "图片不对" });
  const undo = page.getByRole("status").filter({ hasText: "已标记图片不对，6 秒内可以撤回。" });
  await seal.click();
  await markWrong.click();
  await expect(seal).toHaveCount(0);
  await expect(undo.getByRole("button", { name: "撤回" })).toBeVisible();
  await expectNoHorizontalOverflow(page, "390px 撤回胶囊");
  await page.screenshot({ path: resolve(import.meta.dirname, "../../temp/artist-media-shots/admin-390-undo.png"), animations: "disabled" });
  expect(requests).toHaveLength(0);
  await undo.getByRole("button", { name: "撤回" }).click();
  await expect(seal).toBeVisible();
  await expect(undo).toHaveCount(0);
  await page.clock.fastForward(6000);
  expect(requests).toHaveLength(0);

  await seal.click();
  await markWrong.click();
  await expect(undo.getByRole("button", { name: "撤回" })).toBeVisible();
  await page.clock.fastForward(5000);
  expect(requests).toHaveLength(0);
  await page.clock.fastForward(1000);
  await expect.poll(() => requests.length).toBe(1);
  const [request] = requests;
  if (request === undefined) throw new Error("缺少标记请求");
  expectWrite(request, "POST", { expected_revision: revision, field: "image" });
  await expect(undo).toHaveCount(0);
  await expect(seal).toHaveCount(0);
  await expect(page.getByRole("button", { name: "指定对应" })).toHaveCSS("border-top-style", "dashed");
});

test("管理员保存简介并上传图片，刷新后显示人工来源", async ({ page, backend }) => {
  const { detail, url, api } = seed(await backend.start({ signedInAs: "curator" }));
  const revision = detail.revision;
  const text = "青岚室内乐团的合成简介 🎵";
  const imageResource = "synthetic-admin-portrait.png";
  const bioResult = decodeArtistMediaWrite(sampleBody("artists/set-bio"));
  const imageResult = decodeArtistMediaWrite(sampleBody("artists/set-image"));
  await page.route(`**${api}/bio`, (route) => {
    detail.bio = { text, lang: "zh", source: "manual", source_url: null, license: null, license_url: null, citations: [] };
    detail.revision = revision + 1;
    return route.fulfill({ json: { ...bioResult, artist_id: detail.artist.id, revision: detail.revision } });
  });
  await page.route(`**${api}/image`, (route) => {
    detail.image = { resource: imageResource, source: "manual", source_url: null, license: null, license_url: null, author: null, citations: [] };
    detail.artist.image = imageResource;
    detail.revision = revision + 2;
    return route.fulfill({ json: { ...imageResult, artist_id: detail.artist.id, revision: detail.revision } });
  });

  await page.goto(url);
  const bio = page.getByRole("region", { name: "艺人简介" });
  await bio.hover();
  await bio.getByRole("button", { name: "编辑简介" }).click();
  const editor = page.getByRole("textbox", { name: "简介" });
  await expect(editor).toBeFocused();
  await editor.fill(`  ${text}  `);
  await expect(page.getByText(`${Array.from(text).length} / 4000`)).toBeVisible();
  const bioRequest = page.waitForRequest(`**${api}/bio`);
  await editor.press("Control+Enter");
  expectWrite(await bioRequest, "PUT", { expected_revision: revision, text, lang: "zh" });
  await expect(editor).toHaveCount(0);
  await expect(bio.getByText(text)).toBeVisible();
  await expect(bio.getByRole("button", { name: "简介来源：管理员提供" })).toBeVisible();

  const upload = page.waitForRequest(`**${api}/image`);
  const bytes = gradientPng(256, 42);
  await page.locator('input[type="file"]').setInputFiles({ name: "portrait.png", mimeType: "image/png", buffer: bytes });
  const imageRequest = await upload;
  expect(imageRequest.method()).toBe("PUT");
  expect(imageRequest.headers()["idempotency-key"]).toMatch(/^[0-9a-f-]{36}$/);
  expect(imageRequest.headers()["content-type"]).toMatch(/^multipart\/form-data; boundary=.+/);
  const body = imageRequest.postDataBuffer();
  expect(body?.includes(bytes)).toBe(true);
  expect(body?.toString()).toContain(`name="expected_revision"\r\n\r\n${revision + 1}\r\n`);
  expect(body?.toString()).toContain('name="image"; filename="portrait.png"\r\nContent-Type: image/png');
  await expect(page.getByRole("button", { name: "图片来源：管理员提供" })).toBeVisible();
  await expect(page.locator(`img[src="/api/v1/artworks/${imageResource}"]`)).toBeVisible();
});

test("普通成员能查看来源，弹层和页头均无资料写入入口", async ({ page, backend }) => {
  const { url, api } = seed(await backend.start({ signedInAs: "listener" }));
  const writes: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.startsWith(`${api}/`) && request.method() !== "GET") writes.push(request.url());
  });
  await page.goto(url);

  await page.getByRole("button", { name: /^图片来源：Wikimedia Commons/ }).click();
  const source = page.getByRole("dialog", { name: "图片来源" });
  await expect(source.getByRole("link", { name: "Wikimedia Commons" })).toBeVisible();
  await expect(source.getByRole("button")).toHaveCount(0);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: /^简介来源：维基百科/ }).click();
  const bio = page.getByRole("dialog", { name: "简介来源" });
  await expect(bio.getByRole("link", { name: "维基百科" })).toBeVisible();
  await expect(bio.getByRole("button")).toHaveCount(0);
  await page.keyboard.press("Escape");

  await expect(page.getByRole("button", { name: /图片不对|指定对应|更换图片|上传图片|简介不对|编辑简介|写简介/ })).toHaveCount(0);
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await pasteLink(page, "https://www.wikidata.org/wiki/Q42");
  await expect(page.getByRole("dialog", { name: "指定对应" })).toHaveCount(0);
  expect(writes).toEqual([]);
});
