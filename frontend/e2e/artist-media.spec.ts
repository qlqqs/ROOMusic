import { resolve } from "node:path";
import { artistImage } from "../src/test/artist-media-data";
import { seedArtistMedia } from "./support/artist-media-seed";
import { expectNoHorizontalOverflow } from "./support/checks";
import { expect, test } from "./support/fixtures";

const portraitPath = `/api/v1/artworks/${artistImage.resource}`;

for (const width of [390, 1440]) {
  test(`${width}px 艺人图片与长简介可浏览、展开和收起`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const artist = seedArtistMedia(mock, "with-media");
    await page.goto("/artists");
    const link = page.locator("#main").getByRole("link", { name: artist.name, exact: true });
    await expect(link.locator("img")).toHaveAttribute("src", portraitPath);
    await link.click();

    const title = page.getByRole("heading", { name: artist.name, level: 1 });
    const header = page.locator("#main header").filter({ has: title });
    await expect(title).toBeVisible();
    const picture = header.locator("img");
    await expect(picture).toHaveAttribute("src", portraitPath);
    await expect.poll(() => picture.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);

    const bio = header.getByRole("region", { name: "艺人简介" });
    const text = bio.getByText(/^青岚室内乐团在河畔小城成立/);
    const expand = bio.getByRole("button", { name: "展开" });
    const bioSeal = bio.getByRole("button", { name: /^简介来源：维基百科/ });
    const imageSeal = header.getByRole("button", { name: /^图片来源：Wikimedia Commons/ });
    await expect(bioSeal).toBeVisible();
    await expect(imageSeal).toBeVisible();
    await expect(bio.getByText(/简介：|图片：/)).toHaveCount(0);
    await expect(bio.getByRole("link")).toHaveCount(0);
    await expect(expand).toHaveAttribute("aria-expanded", "false");
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    const folded = await text.evaluate((element) => ({
      visibleHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      lineHeight: Number.parseFloat(getComputedStyle(element).lineHeight),
    }));
    expect(folded.visibleHeight).toBeCloseTo(Math.round(folded.lineHeight * 3), 0);
    expect(folded.scrollHeight).toBeGreaterThan(folded.visibleHeight);
    await expectNoHorizontalOverflow(page, `${width}px 折叠`);

    await expand.click();
    const collapse = bio.getByRole("button", { name: "收起" });
    await expect(collapse).toHaveAttribute("aria-expanded", "true");
    await expect.poll(() => text.evaluate((element) => element.clientHeight)).toBe(folded.scrollHeight);
    await expectNoHorizontalOverflow(page, `${width}px 展开`);

    await collapse.click();
    await expect(expand).toHaveAttribute("aria-expanded", "false");
    await expect.poll(() => text.evaluate((element) => element.clientHeight)).toBe(folded.visibleHeight);
    await page.evaluate(() => {
      window.scrollTo(0, 0);
      document.getElementById("main")?.scrollTo(0, 0);
    });
    await page.screenshot({ path: resolve(import.meta.dirname, `../../temp/artist-media-shots/member-${width}.png`), animations: "disabled" });

    await bioSeal.click();
    const bioSource = bio.getByRole("dialog", { name: "简介来源" });
    await expect(bioSource.getByRole("link", { name: "维基百科" })).toBeVisible();
    await expectNoHorizontalOverflow(page, `${width}px 简介来源`);
    await page.keyboard.press("Escape");
    await expect(bioSeal).toBeFocused();

    await imageSeal.click();
    const imageSource = header.getByRole("dialog", { name: "图片来源" });
    await expect(imageSource.getByRole("link", { name: "Wikimedia Commons" })).toBeVisible();
    await expect(imageSource.getByText("合成样本作者")).toBeVisible();
    await page.screenshot({ path: resolve(import.meta.dirname, `../../temp/artist-media-shots/member-${width}-image-source.png`), animations: "disabled" });
    await expectNoHorizontalOverflow(page, `${width}px 图片来源`);
    await page.keyboard.press("Escape");
    await expect(imageSource).toHaveCount(0);
    await expect(imageSeal).toBeFocused();
  });
}

test("390px 图片来源弹层淡入时也没有横向溢出", async ({ page, backend }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  const mock = await backend.start({ signedInAs: "listener" });
  const artist = seedArtistMedia(mock, "with-media");
  await page.goto(`/artists/${artist.id}`);
  // 固定在淡入中的一帧，避免浏览器调度决定能否测到动画期间的溢出。
  await page.addStyleTag({ content: '[role="dialog"][aria-label="图片来源"] { animation-play-state: paused; }' });
  await page.getByRole("button", { name: /^图片来源：Wikimedia Commons/ }).click();
  const dialog = page.getByRole("dialog", { name: "图片来源" });
  await dialog.evaluate((element) => {
    for (const animation of element.getAnimations()) animation.currentTime = 35;
  });
  await page.screenshot({ path: resolve(import.meta.dirname, "../../temp/artist-media-shots/member-390-image-source-opening.png") });
  await expectNoHorizontalOverflow(page, "390px 图片来源淡入中");
});

for (const scenario of ["without-media", "broken-image"] as const) {
  test(scenario === "without-media" ? "无图无简介时显示缩写且不渲染简介区" : "图片加载失败时回到缩写并保留来源印章", async ({ page, backend }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const artist = seedArtistMedia(mock, scenario);
    await page.goto(`/artists/${artist.id}`);

    const title = page.getByRole("heading", { name: artist.name, level: 1 });
    await expect(title).toBeVisible();
    const header = page.locator("#main header").filter({ has: title });
    await expect(header.getByText("青", { exact: true })).toBeVisible();
    await expect(header.locator("img")).toHaveCount(0);
    const bio = header.getByRole("region", { name: "艺人简介" });
    if (scenario === "broken-image") {
      expect(mock.callsTo("GET", portraitPath)).toHaveLength(1);
      await header.getByRole("button", { name: /^图片来源：Wikimedia Commons/ }).click();
      await expect(header.getByRole("dialog", { name: "图片来源" }).getByRole("link", { name: "Wikimedia Commons" })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(bio).toHaveCount(0);
    } else {
      expect(mock.callsTo("GET", portraitPath)).toHaveLength(0);
      await expect(header.getByRole("button", { name: /^图片来源|指定对应/ })).toHaveCount(0);
      await expect(bio).toHaveCount(0);
    }
    await expectNoHorizontalOverflow(page, scenario);
  });
}
