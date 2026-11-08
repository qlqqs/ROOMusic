import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import { installLibraryApi, seedScan } from "../../../test/fake-library";
import { renderApp } from "../../../test/render-app";

describe("扫描记录的发起方标签", () => {
  it.each(["manual", "scheduled"] as const)("%s 只在自动发起时显示“自动”", async (trigger) => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    const library = installLibraryApi(api);
    seedScan(library, { trigger, status: "succeeded", finished_at: "2026-01-02T00:01:00Z" });
    renderApp({ path: "/settings/library" });

    const table = await screen.findByRole("table", { name: "扫描记录" });
    expect(within(table).queryByText("自动") !== null).toBe(trigger === "scheduled");
  });
});
