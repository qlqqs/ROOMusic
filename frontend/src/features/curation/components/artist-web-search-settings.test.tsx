import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { seedException } from "../../../test/curation-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../../../test/fake-api";
import { installCurationApi } from "../../../test/fake-curation";
import { renderApp } from "../../../test/render-app";
import { decodeCurationExceptionPage, decodeCurationSources } from "../api/decoders";

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
  const dependencies = within(sources.getByRole("list", { name: "依赖" }));
  const model = dependencies.getByRole("link", { name: modelReady ? "对话模型已就绪" : "对话模型没有设置，去管家设置" });
  const searchLabel = searchReady ? "搜索来源已就绪" : "搜索来源没有设置，去管家设置";
  const search = dependencies.getByRole("link", { name: searchLabel });
  expect(model.getAttribute("href")).toBe("/settings/agent");
  expect(search.getAttribute("href")).toBe("/settings/agent");
  expect(model.hasAttribute("data-missing")).toBe(!modelReady);
  expect(search.hasAttribute("data-missing")).toBe(!searchReady);
  expect(model.querySelector("svg.lucide-sparkles")).not.toBeNull();
  expect(search.querySelector("svg.lucide-search")).not.toBeNull();
  await user.hover(search);
  expect(await screen.findByText(searchLabel)).toBeTruthy();
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

const artist = { id: "00000000-0000-4000-8000-000000000101", name: "青岚室内乐团" };

it.each([
  { source: "wikidata", label: "Wikidata 条目", id: "Q900001", base: "https://www.wikidata.org/wiki/" },
  { source: "musicbrainz", label: "MusicBrainz 艺人", id: "00000000-0000-4000-8000-000000000102", base: "https://musicbrainz.org/artist/" },
])("例外列表链接艺人和 $label，确认框说明采纳与不采纳的结果", async ({ source, label, id, base }) => {
  const user = userEvent.setup();
  const { curation } = setup();
  seedException(curation, {
    kind: "artist_identity",
    tool: "resolve_artist_identity",
    release: null,
    artist,
    input: { artist_id: artist.id, external_id: id, source },
    preview: { changes: [{ field: "external_link", before: null, after: id }] },
  });
  renderApp({ path: "/settings/curation" });

  const table = within(await screen.findByRole("table", { name: "等你处理的例外" }));
  expect(table.getByRole("columnheader", { name: "对象" })).toBeTruthy();
  expect(table.getByRole("link", { name: artist.name }).getAttribute("href")).toBe(`/artists/${artist.id}`);
  const external = table.getByRole("link", { name: id });
  expect(external.closest("li")?.textContent).toBe(`对应到 ${label} ${id}`);
  expect(external.getAttribute("href")).toBe(base + id);
  expect(external.getAttribute("target")).toBe("_blank");

  await user.click(table.getByRole("button", { name: `采纳对${artist.name}的修改` }));
  const accept = within(screen.getByRole("dialog", { name: "采纳这项修改？" }));
  expect(accept.getByText(artist.name)).toBeTruthy();
  expect(accept.getByRole("link", { name: id }).getAttribute("href")).toBe(base + id);
  expect(accept.getByText("写入这位艺人的外部对应，之后由有许可的来源补图片与简介；同一位艺人的其他候选随之关闭。可以在操作记录里撤销。")).toBeTruthy();
  await user.click(accept.getByRole("button", { name: "取消" }));

  await user.click(table.getByRole("button", { name: `不采纳对${artist.name}的修改` }));
  const decline = within(screen.getByRole("dialog", { name: "不采纳这项修改？" }));
  expect(decline.getByText("以后不再提出这个条目；可以在“不再提出”里清除。")).toBeTruthy();
});

it("解码被同一艺人的其他候选取代的例外", () => {
  const sample = decodeCurationExceptionPage(sampleBody("curation/exceptions"));
  const release = sample.items[0];
  if (release === undefined) throw new Error("缺少自动整理例外样本");
  const item = { ...release, artist, release: null, kind: "artist_identity", tool: "resolve_artist_identity", status: "superseded" };
  expect(decodeCurationExceptionPage({ ...sample, items: [item] }).items).toStrictEqual([item]);
  expect(() => decodeCurationExceptionPage({ ...sample, items: [{ ...item, status: "closed" }] })).toThrow(/items.*status/);
});
