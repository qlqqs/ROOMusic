import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { installFakeApi, installFakeSession, installOverviewApi } from "../../../test/fake-api";
import { installCurationApi } from "../../../test/fake-curation";
import { renderApp } from "../../../test/render-app";
import { decodeCurationSources } from "../api/decoders";

function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  return { api, curation: installCurationApi(api) };
}

it.each([
  { modelReady: true, searchReady: false, enabled: false },
  { modelReady: false, searchReady: false, enabled: true },
  { modelReady: true, searchReady: true, enabled: false },
])("网络搜索模型为 $modelReady、搜索为 $searchReady、开关为 $enabled 时的来源设置", async ({ modelReady, searchReady, enabled }) => {
  const user = userEvent.setup();
  const { api, curation } = setup();
  const source = decodeCurationSources(sampleBody("curation/sources")).items.find(({ id }) => id === "web_search");
  if (source === undefined) throw new Error("缺少网络搜索来源样本");
  curation.sources.push({
    ...source,
    enabled,
    inactive_reason: searchReady ? "source_disabled" : "search_unconfigured",
    requirements: [{ kind: "model", ready: modelReady }, { kind: "search", ready: searchReady }],
  });
  renderApp({ path: "/settings/curation" });

  const sources = within(await screen.findByRole("region", { name: "外部源" }));
  const toggle = await sources.findByRole<HTMLInputElement>("switch", { name: "网络搜索" });
  // 缺的依赖写在这一行的说明里（文字加“去设置”链接），不再用带警示点的图标；都就绪时不写。
  const description = document.getElementById(toggle.getAttribute("aria-describedby") ?? "");
  if (description === null) throw new Error("网络搜索没有说明");
  const missing = [modelReady ? null : "对话模型", searchReady ? null : "搜索来源"].filter((name) => name !== null);
  if (missing.length === 0) {
    expect(description.textContent).toBe("艺人图片、艺人简介");
    expect(within(description).queryByRole("link")).toBeNull();
  } else {
    expect(description.textContent).toContain(`需要先在管家设置里配好${missing.join("和")}。`);
    expect(within(description).getByRole("link", { name: "去设置" }).getAttribute("href")).toBe("/settings/agent");
    // 依赖缺了就不再写同义的“没有选搜索来源”。
    expect(description.textContent).not.toContain("还没有在管家设置里选搜索来源");
  }
  expect(sources.queryByRole("list", { name: "依赖" })).toBeNull();
  expect(toggle.checked).toBe(enabled);
  const ready = modelReady && searchReady;
  expect(toggle.disabled).toBe(!ready && !enabled);

  const path = "/api/v1/admin/curation/sources/web_search";
  await user.click(toggle);
  if (ready || enabled) {
    await waitFor(() => expect(api.requestsTo("PUT", path)[0]?.body).toEqual({ enabled: !enabled }));
    await waitFor(() => expect(toggle.checked).toBe(!enabled));
    if (!ready) await waitFor(() => expect(toggle.disabled).toBe(true));
  } else {
    expect(api.requestsTo("PUT", path)).toHaveLength(0);
  }
});
