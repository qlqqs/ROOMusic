import { expect, test } from "./support/fixtures";
import { artistKeyActivate, artistTabTo } from "./support/artists";
import { seedArtistsE2E } from "./support/artist-seed";
import { expectFocusStaysInDialog } from "./support/keyboard";

const uuid = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/;

// 所有治理/键盘用例统一关键词，主会话可用 --grep 艺人稳定性 --repeat-each=20。
test("艺人稳定性：纯键盘合并、I1 一键拆回旧身份，旧链接恢复", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const seed = seedArtistsE2E(mock);
  await page.goto(`/artists/${seed.artist.id}`);
  await expect(page.getByRole("heading", { name: seed.artist.name, level: 1 })).toBeVisible();
  await artistKeyActivate(page, page.getByRole("button", { name: "合并艺人…", exact: true }));
  const merge = page.getByRole("dialog", { name: "合并艺人", exact: true });
  const search = merge.getByRole("textbox", { name: "查找艺人" });
  await artistTabTo(page, search);
  await page.keyboard.type(seed.source.name);
  await page.keyboard.press("Enter");
  await artistKeyActivate(page, merge.getByRole("radio", { name: new RegExp(seed.source.name) }), "Space");
  await expect(merge.getByRole("status")).toContainText("合并不能撤销，需要时可以再拆开。");
  await artistKeyActivate(page, merge.getByRole("button", { name: "合并", exact: true }));
  await expect(merge).toBeHidden();
  await expect(page.getByText(`已把“${seed.source.name}”并入“${seed.artist.name}”。`, { exact: true })).toBeVisible();
  const merged = mock.callsTo("POST", `/api/v1/artists/${seed.artist.id}/merge`);
  expect(merged).toHaveLength(1);
  expect(merged[0]?.body).toEqual({ expected_revision: 1, source_artist_id: seed.source.id, source_expected_revision: 1 });
  expect(merged[0]?.idempotencyKey).toMatch(uuid);

  await artistKeyActivate(page, page.getByRole("button", { name: "拆开…", exact: true }));
  const split = page.getByRole("dialog", { name: "拆开艺人", exact: true });
  await expect(split.getByRole("heading", { name: "之前并入这里的艺人" })).toBeVisible();
  await artistKeyActivate(page, split.getByRole("button", { name: `拆回 ${seed.source.name}`, exact: true }));
  await expect(split.getByRole("checkbox", { name: seed.source.name, exact: true })).toBeChecked();
  await expect(split.getByRole("radio", { name: new RegExp(`恢复为“${seed.source.name}”`) })).toBeChecked();
  await artistKeyActivate(page, split.getByRole("button", { name: "预览", exact: true }));
  await expect(split.getByRole("status")).toContainText("拆开不能撤销，需要时可以再合并。");
  await artistKeyActivate(page, split.getByRole("button", { name: "拆开", exact: true }));
  await expect(split).toBeHidden();
  const [call] = mock.callsTo("POST", `/api/v1/artists/${seed.artist.id}/split`);
  expect(call?.body).toMatchObject({ mode: "aliases", name_key_ids: seed.sourceKeys, expected_revision: 2, target: { kind: "restore", artist_id: seed.source.id, expected_revision: 2 }, basis: expect.any(String) });
  expect(call?.idempotencyKey).toMatch(uuid);
  expect(mock.callsTo("GET", `/api/v1/artists/${seed.artist.id}/merged-sources`).length).toBeGreaterThan(0);
  expect(mock.callsTo("GET", "/api/v1/operations")).toHaveLength(0);
  // 服务端已经恢复旧身份；若导航仍回到合并目标，这是客户端缓存重定向问题，不能靠整页重载掩盖。
  expect(seed.data.details.get(seed.source.id)?.redirect_artist_id).toBeNull();
  await expect(page.getByRole("link", { name: `打开“${seed.source.name}”`, exact: true })).toHaveAttribute("href", `/artists/${seed.source.id}`);
  await artistKeyActivate(page, page.getByRole("link", { name: `打开“${seed.source.name}”`, exact: true }));
  await expect(page).toHaveURL(new RegExp(`/artists/${seed.source.id}$`));
  await expect(page.getByRole("heading", { name: seed.source.name, level: 1, exact: true })).toBeVisible();
});

test("艺人稳定性：I1 手动选别名默认恢复，管理员可改成新艺人并纯键盘拆开", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const seed = seedArtistsE2E(mock, { merged: true });
  await page.goto(`/artists/${seed.artist.id}`);
  await artistKeyActivate(page, page.getByRole("button", { name: "拆开…", exact: true }));
  const dialog = page.getByRole("dialog", { name: "拆开艺人", exact: true });
  await artistKeyActivate(page, dialog.getByRole("checkbox", { name: seed.source.name, exact: true }), "Space");
  await expect(dialog.getByRole("radio", { name: new RegExp(`恢复为“${seed.source.name}”`) })).toBeChecked();
  // 原生 radio 组仅选中项处于 Tab 序列；用方向键转到“新艺人”，不能用 focus() 绕过。
  await artistTabTo(page, dialog.getByRole("radio", { name: new RegExp(`恢复为“${seed.source.name}”`) }));
  await page.keyboard.press("ArrowUp");
  await expect(dialog.getByRole("radio", { name: "一位新的艺人", exact: true })).toBeChecked();
  await artistTabTo(page, dialog.getByRole("textbox", { name: "新艺人的主名（可不填）" }));
  await page.keyboard.type("独立署名");
  await artistKeyActivate(page, dialog.getByRole("button", { name: "预览", exact: true }));
  await artistKeyActivate(page, dialog.getByRole("button", { name: "拆开", exact: true }));
  await expect(dialog).toBeHidden();
  expect(mock.callsTo("POST", `/api/v1/artists/${seed.artist.id}/split`).at(-1)?.body).toMatchObject({ mode: "aliases", name_key_ids: seed.sourceKeys, target: { kind: "new", display_name: "独立署名" } });
  await expect(page.getByText("已拆出“独立署名”。", { exact: true })).toBeVisible();
});

test("艺人稳定性：I2 纯键盘按专辑拆开，作曲参与、跨页与筛选保留选择", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const seed = seedArtistsE2E(mock);
  const first = seed.albums[0];
  const last = seed.albums[20];
  if (first === undefined || last === undefined) throw new Error("I2 种子必须跨过 20 条一页");
  await page.goto(`/artists/${seed.artist.id}`);
  await artistKeyActivate(page, page.getByRole("button", { name: "拆开…", exact: true }));
  const dialog = page.getByRole("dialog", { name: "拆开艺人", exact: true });
  await artistTabTo(page, dialog.getByRole("radio", { name: "把一些名字拆成另一位艺人", exact: true }));
  await page.keyboard.press("ArrowDown");
  await expect(dialog.getByRole("radio", { name: "按专辑分给另一位同名的人" })).toBeChecked();
  // 用 label 的空白规范化后文字定位，角色必须实际出现在可访问名称中。
  const albumBox = (title: string) => dialog.getByRole("checkbox", { name: new RegExp(`${title}.*作曲`) });
  await expect(albumBox(first.group.representative.title)).toBeVisible();
  await artistKeyActivate(page, albumBox(first.group.representative.title), "Space");
  await expect(albumBox(first.group.representative.title)).toBeChecked();
  await artistKeyActivate(page, dialog.getByRole("button", { name: "下一页", exact: true }));
  await expect(dialog.getByText("第 2 / 2 页", { exact: true })).toBeVisible();
  await artistKeyActivate(page, albumBox(last.group.representative.title), "Space");
  await expect(dialog.getByRole("group", { name: "分出去的专辑（已选 2 张）" })).toBeVisible();
  const filter = dialog.getByRole("textbox", { name: "按专辑名筛选" });
  await artistTabTo(page, filter);
  await page.keyboard.type(first.group.representative.title);
  await page.keyboard.press("Enter");
  await expect(albumBox(first.group.representative.title)).toBeChecked();
  await expect(dialog.getByRole("group", { name: "分出去的专辑（已选 2 张）" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "下一页", exact: true })).toHaveCount(0);
  await artistKeyActivate(page, dialog.getByRole("button", { name: "预览", exact: true }));
  await artistKeyActivate(page, dialog.getByRole("button", { name: "拆开", exact: true }));
  await expect(dialog).toBeHidden();
  const requests = mock.callsTo("GET", `/api/v1/artists/${seed.artist.id}/albums`).map((call) => new URLSearchParams(call.search));
  expect(requests.some((query) => query.get("participation") === "any" && query.get("page") === "2")).toBe(true);
  expect(requests.some((query) => query.get("participation") === "any" && query.get("q") === first.group.representative.title)).toBe(true);
  expect(mock.callsTo("POST", `/api/v1/artists/${seed.artist.id}/split-preview`).at(-1)?.body).toMatchObject({ mode: "albums", group_ids: [first.group.id, last.group.id] });
  expect(mock.callsTo("POST", `/api/v1/artists/${seed.artist.id}/split`).at(-1)?.body).toMatchObject({ mode: "albums", group_ids: [first.group.id, last.group.id], target: { kind: "new" } });
});

test("艺人稳定性：纯键盘改主名与改回默认，Esc 和焦点陷阱不发写请求", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  const seed = seedArtistsE2E(mock);
  await page.goto(`/artists/${seed.artist.id}`);
  const trigger = page.getByRole("button", { name: "修改主名…", exact: true });
  await artistKeyActivate(page, trigger);
  const dialog = page.getByRole("dialog", { name: "修改主名", exact: true });
  await expectFocusStaysInDialog(page, dialog, 6);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
  expect(mock.callsTo("PATCH", `/api/v1/artists/${seed.artist.id}/name`)).toHaveLength(0);
  await page.keyboard.press("Enter");
  await artistTabTo(page, dialog.getByRole("textbox", { name: "主名", exact: true }));
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("邓丽君 · 珍藏");
  await artistKeyActivate(page, dialog.getByRole("button", { name: "保存", exact: true }));
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { name: "邓丽君 · 珍藏", exact: true, level: 1 })).toBeVisible();
  expect(mock.callsTo("PATCH", `/api/v1/artists/${seed.artist.id}/name`)[0]?.body).toEqual({ expected_revision: 1, mode: "set", name: "邓丽君 · 珍藏" });
  await artistKeyActivate(page, trigger);
  await artistTabTo(page, dialog.getByRole("radio", { name: "使用下面的名字" }));
  await page.keyboard.press("ArrowDown");
  await expect(dialog.getByRole("radio", { name: "改回最常见的写法" })).toBeChecked();
  await artistKeyActivate(page, dialog.getByRole("button", { name: "保存", exact: true }));
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { name: seed.artist.name, exact: true, level: 1 })).toBeVisible();
  const calls = mock.callsTo("PATCH", `/api/v1/artists/${seed.artist.id}/name`);
  expect(calls).toHaveLength(2);
  expect(calls[1]?.body).toEqual({ expected_revision: 2, mode: "auto" });
  expect(calls[0]?.idempotencyKey).toMatch(uuid);
  expect(calls[1]?.idempotencyKey).toMatch(uuid);
  expect(calls[0]?.idempotencyKey).not.toBe(calls[1]?.idempotencyKey);
});

test("艺人稳定性：候选只提示、纯键盘忽略与恢复提示", async ({ page, backend }) => {
  const mock = await backend.start({ signedInAs: "curator" });
  seedArtistsE2E(mock);
  await page.goto("/artist-candidates");
  await expect(page.getByText(/同一首曲目的.*写了/)).toBeVisible();
  await artistKeyActivate(page, page.getByRole("button", { name: /^忽略“/ }).first());
  await expect(page.getByText("现在没有需要确认的候选。", { exact: true })).toBeVisible();
  expect(mock.callsTo("POST", /\/candidate-ignore$/)[0]?.body).toMatchObject({ ignored: true, key_pairs: expect.any(Array) });
  await artistKeyActivate(page, page.getByRole("button", { name: "恢复提示", exact: true }));
  await expect(page.getByRole("button", { name: /^忽略“/ })).toBeVisible();
  expect(mock.callsTo("POST", /\/candidate-ignore$/)[1]?.body).toMatchObject({ ignored: false });
  expect(mock.callsTo("POST", /\/revert$/)).toHaveLength(0);
  expect(mock.callsTo("POST", /\/merge$/)).toHaveLength(0);
});
