import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { albumGroup, failure, groupDetailOf, releaseDetailOf, releaseVersions } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { jsonResponse } from "../test/http";
import { albumAdminActions, chooseAlbumAction } from "../test/album-actions";
import { renderApp } from "../test/render-app";

const OPERATION_ID = "00000000-0000-4000-8000-0000000000aa";
const REVERT_ID = "00000000-0000-4000-8000-0000000000bb";

/** 一张专辑的两个版本：扫描把两张碟拆成了两个发行（第二个叫 Disc Two）。 */
function setup(options: { role?: "admin" | "user"; merged?: boolean } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const album = albumGroup(1, { title: "Quartet", album_artist: "Sample Quartet", artist: "Sample Quartet", year: 2001, medium_count: 1, track_count: 2, release_count: 2 });
  const [first, second] = releaseVersions(album, 2, () => ({ title: "Quartet Disc Two" }));
  if (first === undefined || second === undefined) throw new Error("缺少版本");
  const catalog = installCatalogApi(api, { groups: [album] });
  catalog.groupDetails.set(album.id, groupDetailOf(album, [first, second]));
  if (options.merged === true) {
    catalog.releases.set(first.id, releaseDetailOf(first, { merged_releases: [{ id: second.id, title: second.title, operation_id: OPERATION_ID, revertible: true }] }));
  }
  const preview = {
    target: { id: first.id, title: first.title, group_id: album.id },
    source: { id: second.id, title: second.title, group_id: album.id },
    media: [
      { medium_id: "00000000-0000-4000-8000-000000000101", from_release: first.id, title: "Medium", position_after: 1, track_count: 2 },
      { medium_id: "00000000-0000-4000-8000-000000000102", from_release: second.id, title: "Bonus", position_after: 2, track_count: 3 },
    ],
    hidden: { corrections: 2, reviews: 0, default_version: false, external_artwork: false, links: 1, artist_overrides: 0 },
    favorites_moved: 0,
    token: "a".repeat(64),
  };
  api.on("GET", /^\/api\/v1\/releases\/[^/]+\/merge-preview/, () => jsonResponse(200, preview));
  api.on("POST", /^\/api\/v1\/releases\/[^/]+\/merge$/, () => jsonResponse(200, { operation: { id: OPERATION_ID }, release: preview.target }));
  api.on("POST", /^\/api\/v1\/operations\/[^/]+\/revert$/, () =>
    jsonResponse(200, { operation_id: REVERT_ID, reverts_operation_id: OPERATION_ID, target_type: "release", target_id: first.id, revision: 1 }),
  );
  return { api, album, first, second };
}

async function openMergeDialog() {
  await chooseAlbumAction(userEvent.setup(), "并入另一个版本…");
  return screen.findByRole("dialog", { name: "把另一个版本并入这里" });
}

describe("发行整张并入（M7 H）", () => {
  it("管理员从同一专辑的其他版本里选一个：预览写出并入后的碟序与合并期间不生效的内容，确认后带令牌提交，成功后可以撤销", async () => {
    const { api, album, first, second } = setup();
    renderApp({ path: `/albums/${album.id}` });
    const dialog = await openMergeDialog();

    const siblings = within(dialog).getByRole("group", { name: "这张专辑的其他版本" });
    expect(within(siblings).queryByRole("radio", { name: /^Quartet 2001/ })).toBeNull();
    await userEvent.click(within(siblings).getByRole("radio", { name: /Quartet Disc Two/ }));

    const discs = await within(dialog).findByRole("list", { name: "并入之后的碟" });
    expect(within(discs).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "第 1 碟 · 来自《Quartet》 · 2 首",
      "第 2 碟 · Bonus · 来自《Quartet Disc Two》 · 3 首",
    ]);
    expect(within(dialog).getByText("《Quartet Disc Two》上的2 项信息修正、外部资料的对应在合并期间不生效，撤销之后恢复。")).toBeTruthy();
    expect(api.requestsTo("GET", /\/merge-preview/)[0]?.path).toBe(`/api/v1/releases/${first.id}/merge-preview?source=${second.id}`);

    await userEvent.click(within(dialog).getByRole("button", { name: "把《Quartet Disc Two》并入这里" }));
    await waitFor(() => expect(api.requestsTo("POST", /\/merge$/)[0]?.body).toEqual({ source_release_id: second.id, token: "a".repeat(64) }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("已把《Quartet Disc Two》并入这个版本。")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(api.requestsTo("POST", /\/operations\/[^/]+\/revert$/)[0]?.path).toBe(`/api/v1/operations/${OPERATION_ID}/revert`));
    expect(await screen.findByText("已撤销合并，被并入的版本恢复为单独的发行。")).toBeTruthy();
  });

  it("预览之后有扫描（409 merge_changed）：对话框保持打开并说明，预览重新读取", async () => {
    const { api, album } = setup();
    api.on("POST", /^\/api\/v1\/releases\/[^/]+\/merge$/, () => {
      const { status, body } = failure(409, "merge_changed", "预览后发行内容已变化，请重新预览");
      return jsonResponse(status, body);
    });
    renderApp({ path: `/albums/${album.id}` });
    const dialog = await openMergeDialog();
    await userEvent.click(within(within(dialog).getByRole("group", { name: "这张专辑的其他版本" })).getByRole("radio", { name: /Quartet Disc Two/ }));
    await userEvent.click(await within(dialog).findByRole("button", { name: "把《Quartet Disc Two》并入这里" }));
    expect(await within(dialog).findByText("两个版本刚被改动过")).toBeTruthy();
    await waitFor(() => expect(api.requestsTo("GET", /\/merge-preview/).length).toBeGreaterThan(1));
  });

  it("已并入的版本列在管理面板里，“撤销合并”撤销那次操作；成员看不到并入入口", async () => {
    const { api, album } = setup({ merged: true });
    renderApp({ path: `/albums/${album.id}` });
    const merged = await screen.findByRole("list", { name: "已并入这个版本的其他版本" });
    expect(merged.textContent).toContain("已并入《Quartet Disc Two》");
    await userEvent.click(within(merged).getByRole("button", { name: "撤销合并，《Quartet Disc Two》恢复为单独的版本" }));
    await waitFor(() => expect(api.requestsTo("POST", /\/operations\/[^/]+\/revert$/)[0]?.path).toBe(`/api/v1/operations/${OPERATION_ID}/revert`));
    expect(await screen.findByText("已撤销合并，被并入的版本恢复为单独的发行。")).toBeTruthy();
  });

  it("成员的“更多操作”里没有“管理”分组，也就没有并入", async () => {
    const { album } = setup({ role: "user" });
    renderApp({ path: `/albums/${album.id}` });
    expect(await albumAdminActions(userEvent.setup())).toEqual([]);
  });
});
