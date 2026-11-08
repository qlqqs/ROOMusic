import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import { installLibraryApi } from "../../../test/fake-library";
import { jsonResponse } from "../../../test/http";
import { renderApp } from "../../../test/render-app";

const path = "/api/v1/scan-schedule";
const changes = [
  { name: "切换开关", desired: { enabled: false, hour: 4 } },
  { name: "修改整点", desired: { enabled: true, hour: 7 } },
];

function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  const library = installLibraryApi(api);
  return { api, library };
}

async function changeSchedule(enabled: boolean) {
  const toggle = await screen.findByRole("switch", { name: "每天自动扫描" });
  if (enabled) await userEvent.selectOptions(screen.getByRole("combobox", { name: "自动扫描时间" }), "7");
  else await userEvent.click(toggle);
}

async function expectShown(enabled: boolean, hour: number) {
  await waitFor(() => {
    expect(screen.getByRole<HTMLInputElement>("switch", { name: "每天自动扫描" }).checked).toBe(enabled);
    if (enabled) expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "自动扫描时间" }).value).toBe(String(hour));
    else expect(screen.queryByRole("combobox", { name: "自动扫描时间" })).toBeNull();
  });
}

describe("每天自动扫描设置", () => {
  it.each(changes)("$name 以整份 enabled/hour PUT 保存", async ({ desired }) => {
    const { api, library } = setup();
    renderApp({ path: "/settings/library" });
    await changeSchedule(desired.enabled);
    await waitFor(() => expect(library.scanSchedule).toEqual(desired));
    await expectShown(desired.enabled, desired.hour);
    const requests = api.requestsTo("PUT", path);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toEqual(desired);
    expect(requests[0]?.headers.get("Content-Type")).toBe("application/json");
  });

  it.each(changes)("$name 保存失败回到原值，重试沿用失败那次的整份值", async ({ desired }) => {
    const { api } = setup();
    let fail = true;
    api.on("PUT", path, (request) => fail
      ? jsonResponse(503, { error: { code: "database_unavailable", message: "无法保存定时扫描设置" }, request_id: "schedule-retry" })
      : jsonResponse(200, request.body));
    renderApp({ path: "/settings/library" });
    await changeSchedule(desired.enabled);

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("没保存上。")).toBeTruthy();
    await expectShown(true, 4);
    expect(api.requestsTo("PUT", path).map((request) => request.body)).toEqual([desired]);

    fail = false;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    await expectShown(desired.enabled, desired.hour);
    expect(api.requestsTo("PUT", path).map((request) => request.body)).toEqual([desired, desired]);
  });
});
