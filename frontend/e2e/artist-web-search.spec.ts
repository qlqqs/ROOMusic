import { resolve } from "node:path";
import type { Page } from "@playwright/test";
import { decodeArtistDetail, decodeArtistWebSearch, type ArtistWebSearchDTO } from "../src/features/artists/api/decoders";
import { decodeCurationSources } from "../src/features/curation/api/decoders";
import { sampleBody } from "../src/test/api-samples";
import { artistImage } from "../src/test/artist-media-data";
import { artistWebSearchOf } from "../src/test/artist-web-search-data";
import { seedException } from "../src/test/curation-data";
import type { ApiMock } from "./support/api-mock";
import { seedArtistMedia } from "./support/artist-media-seed";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";

function seed(mock: ApiMock, scenario: "with-media" | "without-media") {
  const artist = seedArtistMedia(mock, scenario);
  const detail = mock.artists?.details.get(artist.id);
  if (detail === undefined) throw new Error("缺少艺人详情样本");
  return { detail, url: `/artists/${artist.id}`, statusPath: `/api/v1/artists/${artist.id}/web-search` };
}

async function screenshot(page: Page, width: number, state: string) {
  await page.screenshot({
    path: resolve(import.meta.dirname, `../../temp/artist-media-shots/web-admin-${width}-${state}.png`),
    animations: "disabled",
  });
  await expectNoHorizontalOverflow(page, `${width}px ${state}`);
}

for (const width of [390, 1440]) {
  test(`${width}px 管理员查看网络搜索整理的图片、简介和引用`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const { detail, url, statusPath } = seed(await backend.start({ signedInAs: "curator" }), "with-media");
    const { bio } = decodeArtistDetail(sampleBody("artists/detail-web-search"));
    if (bio === null) throw new Error("缺少网络搜索简介样本");
    const photo = { title: "合成演出照片", url: "https://www.photos.example.test/concert?photo=1" };
    detail.bio = bio;
    detail.image = { ...artistImage, source: "web_search", source_url: null, license: null, license_url: null, author: null, citations: [photo] };
    await page.route(`**${statusPath}`, (route) => route.fulfill({ json: artistWebSearchOf() }));
    await page.goto(url);

    const header = page.locator("#main header").filter({ has: page.getByRole("heading", { level: 1, name: detail.artist.name }) });
    await expect(header.getByText(bio.text)).toBeVisible();
    await expect.poll(() => header.locator("img").evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);

    const photoSeal = header.getByRole("button", { name: "图片来源：网络搜索" });
    await expect(photoSeal.locator("svg.lucide-sparkles")).toBeVisible();
    expect(await photoSeal.evaluate((element) => {
      const probe = document.createElement("span");
      probe.style.color = "var(--text-accent)";
      element.append(probe);
      const accent = getComputedStyle(probe).color;
      probe.remove();
      return getComputedStyle(element).color === accent;
    })).toBe(true);
    await photoSeal.click();
    const photoSource = header.getByRole("dialog", { name: "图片来源" });
    const photoCitation = photoSource.getByRole("link", { name: "photos.example.test/concert" });
    await expect(photoCitation).toHaveAttribute("href", photo.url);
    await expect(photoCitation).toHaveAttribute("title", photo.title);
    await expect(photoCitation).toHaveAttribute("target", "_blank");
    await screenshot(page, width, "image");
    await page.keyboard.press("Escape");
    await expect(photoSeal).toBeFocused();

    await header.getByRole("button", { name: "简介来源：网络搜索" }).click();
    const bioSource = header.getByRole("dialog", { name: "简介来源" });
    const citation = bioSource.getByRole("link", { name: "artists.example.org/profile" });
    await expect(citation).toHaveAttribute("href", "https://artists.example.org/profile");
    await expect(citation).toHaveAttribute("title", "演出名录");
    await expect(citation).toHaveAttribute("target", "_blank");
    await screenshot(page, width, "bio");
  });

  test(`${width}px 网络搜索细弧、候选弹层及采纳请求`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.clock.install();
    const mock = await backend.start({ signedInAs: "curator" });
    const { detail, url, statusPath } = seed(mock, "without-media");
    const pending = decodeArtistWebSearch(sampleBody("artists/web-search"));
    const first = pending.candidates[0];
    if (first === undefined) throw new Error("缺少网络搜索候选样本");
    const candidate = { ...first, name: "青岚室内乐团候选" };
    const exception = seedException(mock.curation, {
      id: candidate.exception_id,
      artist: { id: detail.artist.id, name: detail.artist.name },
      release: null,
      kind: "artist_identity",
      tool: "resolve_artist_identity",
      input: { artist_id: detail.artist.id, external_id: candidate.external_id, source: candidate.source },
      preview: { changes: [{ field: "external_link", before: null, after: candidate.external_id }] },
    });
    let status: ArtistWebSearchDTO = artistWebSearchOf({ state: "running" });
    await page.route(`**${statusPath}`, (route) => route.fulfill({ json: status }));

    const path = `/api/v1/admin/curation/exceptions/${candidate.exception_id}/accept`;
    let finishAccept = () => {};
    const accept = new Promise<void>((resolve) => { finishAccept = resolve; });
    await page.route(`**${path}`, async (route) => {
      await accept;
      status = artistWebSearchOf();
      detail.links.wikidata = candidate.external_id;
      detail.revision += 1;
      detail.image = artistImage;
      detail.artist.image = artistImage.resource;
      await route.fulfill({ json: { exception: { ...exception, status: "accepted" }, operation_ids: [] } });
    });

    await page.goto(url);
    const header = page.locator("#main header").filter({ has: page.getByRole("heading", { level: 1, name: detail.artist.name }) });
    await expect(header.getByRole("button", { name: "正在网络搜索" })).toBeVisible();
    const ring = header.locator("svg[data-indeterminate]");
    await expect(ring).toBeVisible();
    const arc = ring.locator("circle").last();
    await expect(arc).toHaveCSS("stroke-dasharray", "18px, 82px");
    await expect(arc).toHaveCSS("stroke-width", "0.9px");
    await expect(arc).toHaveCSS("animation-duration", "2.4s");
    await page.emulateMedia({ reducedMotion: "reduce" });
    await expect(arc).toHaveCSS("animation-name", "none");
    await screenshot(page, width, "searching");

    status = { ...pending, candidates: [candidate] };
    await page.clock.fastForward(20_000);
    const seal = header.getByRole("button", { name: "有 1 个身份候选等你确认" });
    await expect(seal).toBeVisible();
    await expect(ring).toHaveCount(0);
    await seal.click();
    const dialog = header.getByRole("dialog", { name: "身份候选" });
    await expect(dialog.getByText(candidate.external_id)).toBeVisible();
    await dialog.getByLabel("1 个依据网页").click();
    await expect(dialog.getByRole("link", { name: "artists.example.org/profile" })).toHaveAttribute("href", "https://artists.example.org/profile");
    await screenshot(page, width, "candidates");

    try {
      const requestPromise = page.waitForRequest(`**${path}`);
      await dialog.getByRole("button", { name: `就是 ${candidate.name}` }).click();
      const request = await requestPromise;
      expect(request.method()).toBe("POST");
      expect(new URL(request.url()).pathname).toBe(path);
      expect(request.postData()).toBeNull();
      await expect(dialog.getByLabel("正在处理")).toBeVisible();
      await expect(dialog.getByRole("button", { name: `就是 ${candidate.name}` })).toHaveCount(0);
    } finally {
      finishAccept();
    }
    await expect(dialog).toHaveCount(0);
    await expect(header.getByRole("button", { name: /^图片来源：Wikimedia Commons/ })).toBeVisible();
    await expect(seal).toHaveCount(0);
  });

  test(`${width}px 网络搜索来源行写出缺的依赖并链接管家设置`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "curator" });
    const source = decodeCurationSources(sampleBody("curation/sources")).items.find(({ id }) => id === "web_search");
    if (source === undefined) throw new Error("缺少网络搜索来源样本");
    mock.curation.sources.push({
      ...source,
      enabled: false,
      inactive_reason: "search_unconfigured",
      requirements: [{ kind: "model", ready: true }, { kind: "search", ready: false }],
    });
    await page.goto("/settings/curation");
    const sources = page.getByRole("region", { name: "外部源" });
    const toggle = sources.getByRole("switch", { name: "网络搜索" });
    await expect(toggle).toBeDisabled();
    // 只写缺的那一项，不写已就绪的；不再有带警示点的依赖图标。
    await expect(sources.getByText("需要先在管家设置里配好搜索来源。")).toBeVisible();
    await expect(sources.getByRole("list", { name: "依赖" })).toHaveCount(0);
    const search = sources.getByRole("link", { name: "去设置", exact: true });
    await expect(search).toHaveAttribute("href", "/settings/agent");
    await screenshot(page, width, "settings");

    await search.click();
    await expect(page).toHaveURL("/settings/agent");
  });
}
