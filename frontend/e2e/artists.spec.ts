import { expect, test } from "./support/fixtures";
import { artistKeyActivate, artistMetaSegmentLines, artistScenes, artistTabTo, artistWidths, expectArtistLayout, openArtistScene } from "./support/artists";
import { seedArtistsE2E } from "./support/artist-seed";

for (const width of artistWidths) {
  for (const scene of artistScenes) {
    test(`${width}px 艺人 ${scene}：页面、主内容和对话框不溢出，axe 无违规`, async ({ page, backend }) => {
      await page.setViewportSize({ width, height: 900 });
      const mock = await backend.start({ signedInAs: "curator" });
      const seed = seedArtistsE2E(mock, { merged: scene === "split" || scene === "search", favorite: scene === "favorites" });
      await openArtistScene(page, seed, scene);
      await expectArtistLayout(page, `${scene} @${width}`);
    });
  }
}

test("艺人稳定性：纯键盘从导航浏览首字母分组、查找、排序并进入艺人详情", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const seed = seedArtistsE2E(mock);
  await page.goto("/albums");
  await artistKeyActivate(page, page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "艺人", exact: true }));
  await expect(page).toHaveURL(/\/artists$/);
  for (const name of ["D", "假名", "#"]) await expect(page.getByRole("heading", { name, exact: true, level: 3 })).toBeVisible();
  await artistKeyActivate(page, page.getByRole("navigation", { name: "首字母" }).getByRole("link", { name: "D", exact: true }));
  await expect(page).toHaveURL(/initial=D/);
  await expect(page.getByRole("link", { name: seed.artist.name, exact: true })).toBeVisible();
  const sort = page.getByRole("combobox", { name: "排序", exact: true });
  await artistTabTo(page, sort);
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Tab");
  await expect(page).toHaveURL((url) => url.searchParams.get("sort") === "album_count" && !url.searchParams.has("initial"));
  await expect(page.getByRole("navigation", { name: "首字母" })).toHaveCount(0);
  const search = page.getByRole("textbox", { name: "查找艺人", exact: true });
  await artistTabTo(page, search);
  await page.keyboard.type(seed.artist.name);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL((url) => url.searchParams.get("q") === seed.artist.name);
  await artistKeyActivate(page, page.locator("#main").getByRole("link", { name: seed.artist.name, exact: true }));
  await expect(page.getByRole("heading", { name: seed.artist.name, level: 1, exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "参与的曲目", exact: true })).toBeVisible();
  await expect(page.getByText("文件缺失", { exact: true })).toBeVisible();
  const missing = seed.tracks.find((track) => !track.present);
  if (missing === undefined) throw new Error("艺人种子缺少文件缺失曲目");
  await expect(page.getByRole("button", { name: `播放 ${missing.title}`, exact: true })).toBeDisabled();
});

test("普通成员没有艺人管理入口，候选页不请求管理接口", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const seed = seedArtistsE2E(mock);
  await page.goto(`/artists/${seed.artist.id}`);
  await expect(page.getByRole("heading", { name: seed.artist.name, exact: true, level: 1 })).toBeVisible();
  for (const name of ["合并艺人…", "拆开…", "修改主名…"]) await expect(page.getByRole("button", { name, exact: true })).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "可能是同一位", exact: true })).toHaveCount(0);
  await page.goto("/artist-candidates");
  await expect(page.getByRole("heading", { name: "需要管理员权限", exact: true })).toBeVisible();
  expect(mock.callsTo("GET", "/api/v1/artist-candidates")).toHaveLength(0);
  expect(mock.callsTo("GET", /\/merged-sources$/)).toHaveLength(0);
  expect(mock.callsTo("POST", /\/artists\//)).toHaveLength(0);
});

for (const path of ["/artists", "detail", "/me/favorites?tab=artists", "/artist-candidates"]) {
  test(`整理中 ${path}：只显示专用提示，ready 后同页恢复`, async ({ page, backend }) => {
    await page.clock.install();
    const mock = await backend.start({ signedInAs: "curator" });
    const seed = seedArtistsE2E(mock, { favorite: true });
    seed.data.state = "preparing";
    await page.goto(path === "detail" ? `/artists/${seed.artist.id}` : path);
    const hint = page.getByText("艺人信息正在整理，请稍后再试。", { exact: true });
    await expect(hint).toBeVisible();
    await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "艺人", exact: true })).toBeVisible();
    await expect(page.getByText("资料库里还没有艺人。", { exact: true })).toHaveCount(0);
    seed.data.state = "ready";
    seed.data.epoch += 1;
    await page.clock.fastForward(3_100);
    await expect(hint).toHaveCount(0);
    if (path === "detail") await expect(page.getByRole("heading", { name: seed.artist.name, exact: true, level: 1 })).toBeVisible();
    else if (path === "/artist-candidates") await expect(page.getByRole("button", { name: /^忽略“/ })).toBeVisible();
    else await expect(page.locator("#main").getByRole("link", { name: seed.artist.name, exact: true })).toBeVisible();
  });
}

test("艺人 unavailable 隐藏入口；preparing 的专辑署名是纯文字，ready 后才可点", async ({ page, backend }) => {
  await page.clock.install();
  const mock = await backend.start({ signedInAs: "listener" });
  const seed = seedArtistsE2E(mock);
  seed.data.state = "unavailable";
  await page.goto("/me/favorites");
  await expect(page.getByRole("heading", { name: "收藏的专辑", exact: true })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "收藏分类" }).getByRole("link", { name: "艺人", exact: true })).toHaveCount(0);
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "艺人", exact: true })).toHaveCount(0);
  seed.data.state = "preparing";
  const group = mock.groups[0];
  if (group === undefined) throw new Error("艺人种子缺少专辑");
  await page.goto(`/albums/${group.id}`);
  await expect(page.getByRole("heading", { name: group.representative.title, exact: true, level: 1 })).toBeVisible();
  await expect(page.locator("#main").getByText(seed.artist.name, { exact: true }).first()).toBeVisible();
  await expect(page.locator(`#main a[href="/artists/${seed.artist.id}"]`)).toHaveCount(0);
  await expect(page.getByText("艺人信息正在整理，请稍后再试。", { exact: true })).toHaveCount(0);
  seed.data.state = "ready";
  seed.data.epoch += 1;
  await page.clock.fastForward(3_100);
  await expect(page.locator(`#main a[href="/artists/${seed.artist.id}"]`).first()).toBeVisible();
});

test("艺人稳定性：收藏、别名搜索打开同一艺人，取消收藏后刷新列表", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const seed = seedArtistsE2E(mock, { merged: true });
  await page.goto(`/artists/${seed.artist.id}`);
  const heart = page.getByRole("button", { name: `收藏“${seed.artist.name}”`, exact: true });
  await expect(heart).toHaveAttribute("aria-pressed", "false");
  await artistKeyActivate(page, heart);
  await expect(heart).toHaveAttribute("aria-pressed", "true");
  await openArtistScene(page, seed, "favorites");
  await expect(page.getByRole("navigation", { name: "收藏分类" }).getByRole("link", { name: "艺人", exact: true })).toHaveAttribute("aria-current", "page");
  await openArtistScene(page, seed, "search");
  await expect(page.getByText(`别名：${seed.query}`, { exact: true }).first()).toBeVisible();
  await artistKeyActivate(page, page.locator("#main").getByRole("link", { name: seed.artist.name, exact: true }).first());
  await expect(page).toHaveURL(new RegExp(`/artists/${seed.artist.id}$`));
  await expect(heart).toHaveAttribute("aria-pressed", "true");
  await artistKeyActivate(page, heart);
  await expect(heart).toHaveAttribute("aria-pressed", "false");
  await page.goto("/me/favorites?tab=artists");
  await expect(page.getByText("还没有收藏的艺人。", { exact: false })).toBeVisible();
  expect(mock.callsTo("PUT", `/api/v1/me/favorites/artists/${seed.artist.id}`)).toHaveLength(1);
  expect(mock.callsTo("DELETE", `/api/v1/me/favorites/artists/${seed.artist.id}`)).toHaveLength(1);
});

for (const width of artistWidths) {
  test(`${width}px 艺人列表布局：概况只在“·”“、”处换行，首字母导航最后一行不只剩一项${width === 1440 ? "，1440 下一行排完" : ""}`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const seed = seedArtistsE2E(mock);
    await page.goto("/artists");
    await expect(page.getByRole("heading", { name: "全部艺人", exact: true })).toBeVisible();
    await expect(page.locator("#main").getByRole("link", { name: seed.artist.name, exact: true })).toBeVisible();
    await page.evaluate(() => document.fonts.ready);

    // 概况：每一段（数量、身份名）都只占一行，不会出现“曲目艺 / 人”。
    const segments = await artistMetaSegmentLines(page.locator("#main"));
    expect(segments.length, "列表里应有概况段").toBeGreaterThan(0);
    expect(segments.filter(([, lines]) => lines !== 1), `${width}px 段内换行`).toEqual([]);

    // 首字母导航：按每项的上沿分行；1440 下只有一行，其他宽度最后一行至少两项。
    const tops = await page.getByRole("navigation", { name: "首字母" }).getByRole("link").evaluateAll((links) => links.map((link) => Math.round(link.getBoundingClientRect().top)));
    expect(tops).toHaveLength(29);
    const rows = [...new Set(tops)].sort((a, b) => a - b);
    const lastRow = tops.filter((top) => top === rows.at(-1)).length;
    if (width === 1440) expect(rows, "1440 下首字母导航一行排完").toHaveLength(1);
    else expect(lastRow, `${width}px 首字母导航最后一行只剩一项`).toBeGreaterThan(1);
  });
}
