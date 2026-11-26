import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { RootListItemDTO } from "../features/library/api/decoders";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { installLibraryApi } from "../test/fake-library";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const realtimeLabel = "实时监听：文件有改动，几秒内重扫那个文件夹";
const hourlyLabel = "定时扫描，每小时";
const retryLabel = "没保存上扫描间隔，点按重试";
const root: RootListItemDTO = {
  id: "00000000-0000-4000-8000-000000000001",
  name: "合成曲库",
  path: "合成曲库",
  status: "active",
  revision: 1,
  excluded_paths: [],
  scan_watch_mode: "scheduled",
  poll_interval_minutes: 60,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};
const intervalPath = `/api/v1/library-roots/${root.id}/scan-interval`;
const intervalMenu = () => within(screen.getByRole("menu", { name: "定时扫描间隔" }));

function setup(roots = [{ ...root }]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  const library = installLibraryApi(api, { roots });
  renderApp({ path: "/settings/library" });
  return { api, library };
}

describe("音乐目录的监听方式", () => {
  it("实时监听图标有读屏名，停用和待首次扫描的目录行隐藏监听方式", async () => {
    setup([
      { ...root, scan_watch_mode: "realtime" },
      {
        ...root,
        id: "00000000-0000-4000-8000-000000000002",
        name: "停用曲库",
        path: "停用曲库",
        status: "disabled",
      },
      {
        ...root,
        id: "00000000-0000-4000-8000-000000000003",
        name: "待首次扫描",
        path: "待首次扫描",
        scan_watch_mode: null,
      },
    ]);

    const roots = within(await screen.findByRole("list", { name: "音乐目录列表" }));
    const rowOf = (name: string) => {
      const row = roots.getByText(name, { exact: true }).closest("li");
      if (row === null) throw new Error(`没有找到目录 ${name} 所在的行`);
      return within(row);
    };
    expect(rowOf("合成曲库").getByRole("img", { name: realtimeLabel })).toBeTruthy();
    for (const name of ["停用曲库", "待首次扫描"]) {
      const row = rowOf(name);
      expect(row.queryByRole("img", { name: /^实时监听/ })).toBeNull();
      expect(row.queryByRole("button", { name: /^定时扫描/ })).toBeNull();
    }
  });

  it("菜单显示五档和当前勾选，选当前档不发送 PUT，改选后显示保存值", async () => {
    const user = userEvent.setup();
    const { api, library } = setup();
    const hourly = await screen.findByRole("button", { name: hourlyLabel });
    await user.click(hourly);

    const menu = intervalMenu();
    const options = menu.getAllByRole("menuitem");
    expect(options.map((option) => option.textContent)).toEqual(["每 15 分钟", "每小时", "每 4 小时", "每 12 小时", "每天"]);
    const current = menu.getByRole("menuitem", { name: "每小时" });
    expect(options.filter((option) => option.querySelector(".lucide-check") !== null)).toEqual([current]);

    await user.click(current);
    expect(screen.queryByRole("menu", { name: "定时扫描间隔" })).toBeNull();
    expect(api.requestsTo("PUT", intervalPath)).toHaveLength(0);

    await user.click(hourly);
    await user.click(intervalMenu().getByRole("menuitem", { name: "每 4 小时" }));
    await waitFor(() => expect(library.roots[0]?.poll_interval_minutes).toBe(240));
    const updated = await screen.findByRole("button", { name: "定时扫描，每 4 小时" });
    expect(api.requestsTo("PUT", intervalPath).map((request) => request.body)).toEqual([{ poll_interval_minutes: 240 }]);

    await user.click(updated);
    expect(intervalMenu().getByRole("menuitem", { name: "每 4 小时" }).querySelector(".lucide-check")).not.toBeNull();
    expect(intervalMenu().getByRole("menuitem", { name: "每小时" }).querySelector(".lucide-check")).toBeNull();
  });

  it("保存失败时保留原间隔，从重试按钮重发相同的值", async () => {
    const user = userEvent.setup();
    const { api } = setup();
    let fail = true;
    api.on("PUT", intervalPath, (request) => fail
      ? jsonResponse(503, { error: { code: "database_unavailable", message: "服务暂不可用" }, request_id: "scan-interval-retry" })
      : jsonResponse(200, request.body));

    await user.click(await screen.findByRole("button", { name: hourlyLabel }));
    await user.click(intervalMenu().getByRole("menuitem", { name: "每 12 小时" }));
    const retry = await screen.findByRole("button", { name: retryLabel });
    expect(screen.getByRole("button", { name: hourlyLabel })).toBeTruthy();
    const bodies = () => api.requestsTo("PUT", intervalPath).map((request) => request.body);
    const desired = { poll_interval_minutes: 720 };
    expect(bodies()).toEqual([desired]);

    fail = false;
    await user.click(retry);
    await waitFor(() => expect(bodies()).toEqual([desired, desired]));
    expect(await screen.findByRole("button", { name: "定时扫描，每 12 小时" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: retryLabel })).toBeNull();
  });
});
