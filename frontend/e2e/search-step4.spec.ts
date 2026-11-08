import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page } from "@playwright/test";
import { PERSONAL_LISTENER_ID, personalUserOf } from "../src/test/personal-data";
import { expectNoDocumentScroll, expectNoHorizontalOverflow, expectVisibleFocusRing, widths } from "./support/checks";
import { expect, test } from "./support/fixtures";
import {
  chooseTrackSuggestionStep4, keyboardMenuStep4, keyboardTargetStep4, locatedTrackStep4,
  queueSnapshotStep4, SEARCH_STEP4_CREDIT, SEARCH_STEP4_PLAYLIST, searchSuggestionsStep4,
  seedSearchStep4, textLineMetricsStep4, visibleSearchStep4,
} from "./support/search-step4";

async function expectLocated(page: Page, fixture: ReturnType<typeof seedSearchStep4>) {
  await expect(page).toHaveURL((url) => url.pathname === `/albums/${fixture.track.group_id}`
    && url.searchParams.get("release") === fixture.track.release.id && url.searchParams.get("track") === fixture.target.id);
  const row = locatedTrackStep4(page, fixture.target.title);
  await expect(row).toBeFocused();
  // 与仅检查 display/visibility 不同，必须完整落在视口及主滚动容器的裁剪范围内。
  await expect(row).toBeInViewport({ ratio: 1 });
  return row;
}

async function expectAxeClean(page: Page, label: string) {
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(violations.map(({ id, nodes }) => ({ id, targets: nodes.map((node) => node.target) })), label).toEqual([]);
}

async function layout(page: Page, label: string) {
  await expectNoHorizontalOverflow(page, label);
  await expectNoDocumentScroll(page, label);
  await expectAxeClean(page, label);
}

async function highlightStyle(row: Locator) {
  return row.evaluate((element) => {
    const style = getComputedStyle(element);
    const animations = element.getAnimations();
    return { background: style.backgroundColor, shadow: style.boxShadow, animation: style.animationName,
      animations: animations.filter((animation) => animation.playState === "running").length,
      cssAnimations: animations.filter((animation) => animation instanceof CSSAnimation).length,
      cssTransitions: animations.filter((animation) => animation instanceof CSSTransition).length };
  });
}

// 文字 Range 而非 span/grid 盒子：时长贴右边时，只有实际字形占用的矩形能暴露焦点环压字。
async function locatedTextGeometry(row: Locator) {
  return row.evaluate((element) => {
    function rectOf(rect: DOMRect) {
      return { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom };
    }
    function textRects(root: Element) {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      const result = [];
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        if (!node.textContent?.trim()) continue;
        const range = document.createRange();
        range.selectNodeContents(node);
        for (const rect of range.getClientRects()) {
          if (rect.width > 0 && rect.height > 0) result.push({ text: node.textContent, ...rectOf(rect) });
        }
      }
      return result;
    }
    const style = getComputedStyle(element);
    const offset = Number.parseFloat(style.outlineOffset);
    const width = Number.parseFloat(style.outlineWidth);
    const box = element.getBoundingClientRect();
    const inner = { left: box.left - offset, top: box.top - offset,
      right: box.right + offset, bottom: box.bottom + offset };
    const outer = { left: inner.left - width, top: inner.top - width,
      right: inner.right + width, bottom: inner.bottom + width };
    const previous = element.previousElementSibling;
    const next = element.nextElementSibling;
    if (!previous || !next) throw new Error("夹具必须同时有上、下相邻曲目");
    const number = Array.from(element.children).find((child) => child.textContent === "19");
    const duration = Array.from(element.children).find((child) => child.textContent === "4:54");
    if (!number || !duration) throw new Error("定位夹具必须包含曲目号 19 与时长 4:54");
    return {
      offset, width, outlineStyle: style.outlineStyle, inner,
      number: textRects(number), duration: textRects(duration),
      previous: textRects(previous), next: textRects(next),
      // 四条实际环带，不能把整个外矩形当作实心焦点环。
      bands: [
        { ...outer, bottom: inner.top }, { ...outer, top: inner.bottom },
        { left: outer.left, right: inner.left, top: inner.top, bottom: inner.bottom },
        { left: inner.right, right: outer.right, top: inner.top, bottom: inner.bottom },
      ],
    };
  });
}

function expectTextClearOfOutline(geometry: Awaited<ReturnType<typeof locatedTextGeometry>>, phase: string) {
  expect(geometry.outlineStyle, `${phase} 必须真的显示焦点环`).toBe("solid");
  expect(geometry.width).toBeGreaterThan(0);
  for (const name of ["number", "duration"] as const) {
    expect(geometry[name].length, `${phase} ${name} 必须测到文字`).toBeGreaterThan(0);
    for (const rect of geometry[name]) {
      const gaps = { left: rect.left - geometry.inner.left, right: geometry.inner.right - rect.right,
        top: rect.top - geometry.inner.top, bottom: geometry.inner.bottom - rect.bottom };
      for (const [side, gap] of Object.entries(gaps)) {
        expect(gap, `${phase} ${name}「${rect.text}」${side} 距 outline 内边至少 2px（offset=${geometry.offset}px）`).toBeGreaterThanOrEqual(2);
      }
    }
  }
  for (const name of ["previous", "next"] as const) {
    expect(geometry[name].length, `${phase} ${name} 必须测到相邻行文字`).toBeGreaterThan(0);
    for (const rect of geometry[name]) {
      for (const band of geometry.bands) {
        const intersects = rect.left < band.right && rect.right > band.left
          && rect.top < band.bottom && rect.bottom > band.top;
        expect(intersects, `${phase} ${name}「${rect.text}」不与焦点环相交：${JSON.stringify({ rect, band })}`).toBe(false);
      }
    }
  }
}

for (const width of [1440, 375]) {
  for (const reducedMotion of ["no-preference", "reduce"] as const) {
    test(`${width}px ${reducedMotion}：焦点环文字安全间距、高亮前后不跳动且不压相邻行`, async ({ page, backend }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ reducedMotion });
      await page.clock.install();
      const mock = await backend.start({ signedInAs: "listener" });
      const fixture = seedSearchStep4(mock);
      // 先渲染同一专辑并加载字体，再经键盘建议定位，避免字体替换混入布局测量。
      await page.goto(fixture.releaseURL);
      await expect(page.getByRole("heading", { name: fixture.track.release.title, exact: true, level: 1 })).toBeVisible();
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      await chooseTrackSuggestionStep4(page, fixture.target.title);
      const row = await expectLocated(page, fixture);
      await page.clock.pauseAt(await page.evaluate(() => Date.now() + 50));
      await expect(row).toHaveAttribute("data-located", "");
      const highlighted = await locatedTextGeometry(row);
      await testInfo.attach("highlighted-geometry", { body: JSON.stringify(highlighted, null, 2), contentType: "application/json" });
      expectTextClearOfOutline(highlighted, "高亮期间");
      await page.clock.fastForward(3_000);
      await expect(row).not.toHaveAttribute("data-located", "");
      await expect(row).toBeFocused();
      const finished = await locatedTextGeometry(row);
      await testInfo.attach("finished-geometry", { body: JSON.stringify(finished, null, 2), contentType: "application/json" });
      expectTextClearOfOutline(finished, "高亮结束后");
      expect(finished.number, "曲目号全部文字矩形的四边坐标不变").toEqual(highlighted.number);
      expect(finished.duration, "时长全部文字矩形的四边坐标不变").toEqual(highlighted.duration);
    });
  }
}

for (const width of [375, 1440]) {
  test(`${width}px 定位：纯键盘选择曲目建议后焦点落行，继续 Tab 收藏且不播放`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const fixture = seedSearchStep4(mock);
    await page.goto("/albums");
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    const before = await queueSnapshotStep4(page);
    await chooseTrackSuggestionStep4(page, fixture.target.title);
    const row = await expectLocated(page, fixture);
    await expectVisibleFocusRing(page, "定位后的曲目行");
    await page.keyboard.press("Tab");
    const play = row.getByRole("button", { name: "从这首开始播放", exact: true });
    await expect(play).toBeFocused();
    await expect(play).toBeEnabled();
    await page.keyboard.press("Tab");
    const heart = row.getByRole("button", { name: `收藏《${fixture.target.title}》`, exact: true });
    await expect(heart).toBeFocused();
    await expect(heart).toHaveAttribute("aria-pressed", "false");
    await expect(heart).not.toHaveAttribute("aria-busy", "true");
    await page.keyboard.press("Enter");
    await expect(heart).toHaveAttribute("aria-pressed", "true");
    await expect(heart).not.toHaveAttribute("aria-busy", "true");
    expect(personalUserOf(mock.personal, PERSONAL_LISTENER_ID).tracks.has(fixture.target.id)).toBe(true);
    expect(await queueSnapshotStep4(page)).toEqual(before);
    expect(mock.callsTo("GET", /\/playback$/)).toHaveLength(0);
    await expect(page.getByRole("region", { name: "播放器", exact: true })).toHaveCount(0);
    await layout(page, "多碟目标曲目");
  });
}

test("定位：已有非空队列时选择曲目建议，不替换队列或触发播放", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const fixture = seedSearchStep4(mock);
  await page.goto("/search?q=爱");
  const result = page.getByRole("table", { name: "曲目结果" }).getByRole("row").filter({ has: page.getByText(fixture.target.title, { exact: true }) });
  await keyboardMenuStep4(page, result.getByRole("button", { name: "更多操作", exact: true }), "加入队列");
  await expect(result.getByRole("status")).toHaveText("已加入队列。");
  const before = await queueSnapshotStep4(page);
  expect(Object.keys(before), "必须真的已有队列，不能拿两个空对象声称保护了已有队列").toHaveLength(1);
  expect(Object.values(before)[0]).toContain(fixture.target.id);
  await chooseTrackSuggestionStep4(page, fixture.target.title);
  await expectLocated(page, fixture);
  expect(await queueSnapshotStep4(page)).toEqual(before);
  expect(mock.callsTo("GET", /\/playback$/)).toHaveLength(0);
  await expect(page.getByRole("region", { name: "播放器", exact: true }).getByRole("button", { name: "队列（1）", exact: true })).toBeVisible();
});

test("定位：后退、前进与 reload 都重新聚焦并滚到目标曲目", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const fixture = seedSearchStep4(mock);
  await page.goto("/albums");
  await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
  await chooseTrackSuggestionStep4(page, fixture.target.title);
  await expectLocated(page, fixture);
  // 离开详情，再从相邻历史项回来；不能只在第一次挂载时定位。
  await page.keyboard.press("/");
  await expect(visibleSearchStep4(page)).toBeFocused();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("爱");
  await expect(searchSuggestionsStep4(page).getByRole("option")).toHaveCount(3);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/search\?q=/);
  await expect(page.getByRole("table", { name: "曲目结果" })).toBeVisible();
  await page.goBack();
  await expectLocated(page, fixture);
  await page.goBack();
  await expect(page).toHaveURL(/\/albums$/);
  await page.goForward();
  await expectLocated(page, fixture);
  await page.reload();
  await expectLocated(page, fixture);
  expect(mock.callsTo("GET", /\/playback$/)).toHaveLength(0);
});

test("定位：多碟长专辑原本在屏幕外的末碟曲目，同路径建议导航也能滚到", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "listener" });
  const fixture = seedSearchStep4(mock);
  await page.goto(fixture.releaseURL);
  const row = locatedTrackStep4(page, fixture.target.title);
  await expect(page.getByRole("heading", { name: fixture.track.release.title, exact: true, level: 1 })).toBeVisible();
  await expect(row).toBeAttached();
  await expect(row).not.toBeInViewport();
  expect(fixture.detail.media).toHaveLength(4);
  expect(fixture.detail.media.flatMap((medium) => medium.tracks)).toHaveLength(80);
  await chooseTrackSuggestionStep4(page, fixture.target.title);
  await expectLocated(page, fixture);
  // 不是定位到第一张或标题上：行所属的介质必须确实是第四张。
  await expect(page.getByRole("region", { name: /^第 4 张/ }).getByText(fixture.target.title, { exact: true })).toBeInViewport();
});

test("定位：reducedMotion reduce 静态高亮，无动画，计时结束移除且保留焦点", async ({ page, backend }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.clock.install();
  const mock = await backend.start({ signedInAs: "listener" });
  const fixture = seedSearchStep4(mock);
  await page.goto(fixture.locatedURL);
  const row = await expectLocated(page, fixture);
  await page.clock.pauseAt(await page.evaluate(() => Date.now() + 50));
  await expect(row).toHaveAttribute("data-located", "");
  const initial = await highlightStyle(row);
  expect(initial.animation).toBe("none");
  expect(initial.animations).toBe(0);
  // 全局减少动态效果把时长设为 0.01ms；闲置的非零时长不代表存在动画或过渡，检查浏览器实际创建的对象。
  expect(initial.cssAnimations).toBe(0);
  expect(initial.cssTransitions).toBe(0);
  expect(initial.background).not.toBe("rgba(0, 0, 0, 0)");
  expect(initial.shadow).not.toBe("none");
  await page.clock.runFor(500);
  await expect(row).toHaveAttribute("data-located", "");
  expect(await highlightStyle(row)).toEqual(initial);
  await page.clock.fastForward(3_000);
  await expect(row).not.toHaveAttribute("data-located", "");
  const finished = await highlightStyle(row);
  expect(finished.background).not.toBe(initial.background);
  expect(finished.shadow).toBe("none");
  expect(finished.animations).toBe(0);
  expect(finished.cssAnimations).toBe(0);
  expect(finished.cssTransitions).toBe(0);
  await expect(row).toBeFocused();
  await expect(row).toBeInViewport({ ratio: 1 });
});

for (const width of [320, ...widths]) {
  test(`${width}px：专辑匹配原文的真实可见文字最多两行，建议与结果无溢出且 axe 无违规`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const { album } = seedSearchStep4(mock);
    await page.goto("/albums");
    await expect(page.getByRole("heading", { name: "专辑", exact: true })).toBeVisible();
    await page.keyboard.press("/");
    await expect(visibleSearchStep4(page)).toBeFocused();
    await page.keyboard.type("爱");
    await expect(searchSuggestionsStep4(page).getByRole("option")).toHaveCount(3);
    await layout(page, "长署名搜索建议");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("table", { name: "曲目结果" })).toBeVisible();
    await page.evaluate(() => document.fonts.ready.then(() => undefined));
    const noteText = `作曲：${SEARCH_STEP4_CREDIT}`;
    const card = page.locator("#main").getByRole("link").filter({ has: page.getByText(noteText, { exact: true }) });
    const note = card.getByText(noteText, { exact: true });
    await expect(note).toHaveText(noteText);
    expect(await note.textContent()).toBe(noteText);
    const lines = await textLineMetricsStep4(note);
    expect(lines.all, "夹具原文必须实际排出超过两行，防止短样本假通过").toBeGreaterThan(2);
    expect(lines.visible, "按真实 Range 并排除 line-clamp 裁剪部分计数").toBe(2);
    expect(lines.clamp).toBe("2");
    const representative = album.group.representative;
    const fullName = `${representative.title} ${representative.album_artist} 2026 · 3 首曲目 ${noteText}`.replace(/\s+/g, " ").trim();
    await expect(card).toHaveAccessibleName(fullName);
    await layout(page, "两行匹配说明与个人动作");
  });
}

for (const width of [375, 1440]) {
  test(`${width}px：搜索结果行纯键盘收藏与加入已有歌单，不改变队列`, async ({ page, backend }) => {
    await page.setViewportSize({ width, height: 900 });
    const mock = await backend.start({ signedInAs: "listener" });
    const fixture = seedSearchStep4(mock);
    await page.goto("/search?q=爱");
    const row = page.getByRole("table", { name: "曲目结果" }).getByRole("row").filter({ has: page.getByText(fixture.target.title, { exact: true }) });
    const heart = row.getByRole("button", { name: `收藏《${fixture.target.title}》`, exact: true });
    await expect(heart).toHaveAttribute("aria-pressed", "false");
    await expect(heart).not.toHaveAttribute("aria-busy", "true");
    const before = await queueSnapshotStep4(page);
    await keyboardTargetStep4(page, heart, false);
    await expectVisibleFocusRing(page, "搜索结果收藏");
    await page.keyboard.press("Enter");
    await expect(heart).toHaveAttribute("aria-pressed", "true");
    await expect(heart).not.toHaveAttribute("aria-busy", "true");
    expect(personalUserOf(mock.personal, PERSONAL_LISTENER_ID).tracks.has(fixture.target.id)).toBe(true);
    await keyboardMenuStep4(page, row.getByRole("button", { name: "更多操作", exact: true }), "添加到歌单…");
    const dialog = page.getByRole("dialog", { name: "添加到歌单", exact: true });
    await expect(dialog).toContainText(`《${fixture.target.title}》`);
    await expectAxeClean(page, "搜索行添加歌单对话框");
    await keyboardTargetStep4(page, dialog.getByRole("button", { name: new RegExp(`^${SEARCH_STEP4_PLAYLIST}`) }));
    await expect(dialog).toBeHidden();
    await expect(row.getByRole("status")).toContainText(SEARCH_STEP4_PLAYLIST);
    expect(fixture.playlist.items.map((item) => item.track_id)).toEqual([fixture.target.id]);
    expect(mock.callsTo("PUT", new RegExp(`/favorites/tracks/${fixture.target.id}$`))).toHaveLength(1);
    expect(mock.callsTo("POST", new RegExp(`/playlists/${fixture.playlist.state.id}/items$`))).toHaveLength(1);
    expect(mock.callsTo("GET", /\/playback$/)).toHaveLength(0);
    expect(await queueSnapshotStep4(page)).toEqual(before);
    await layout(page, "结果行完成收藏与添加歌单");
  });
}
