import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { decodeCurationSource } from "../features/curation/api/decoders";
import { decodeInstalledPlugin, decodePluginPreview } from "../features/curation/api/plugins";
import { loadApiSample, sampleBody } from "../test/api-samples";
import { failure, type ApiResult } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCurationApi } from "../test/fake-curation";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const base = "/api/v1/admin/plugins";
const installed = decodeInstalledPlugin(sampleBody("plugins/install"));
const source = decodeCurationSource(sampleBody("plugins/enable-source"));
const preview = decodePluginPreview(sampleBody("plugins/preview"));
const name = installed.manifest.name;
const upstream = "https://lyrics.example.org/api";
const installTitle = `安装插件“${name}”？`;
const deleteTitle = `删除插件“${name}”？`;
const deletePath = `${base}/${installed.manifest.id}`;
const sourcePath = `/api/v1/admin/curation/sources/${encodeURIComponent(source.id)}`;
const empty = /^还没有安装插件。.*沙箱.*声明的地址/;
const enabledSourceWithOptionalKey = {
  ...source,
  active: false,
  inactive_reason: "curation_disabled",
  keys: [{ name: "personal", optional: true, configured: false, last_four: "", status: "unchecked" }],
} satisfies typeof source;
const previewWithKeys = {
  ...preview,
  manifest: {
    ...preview.manifest,
    keys: [
      { name: "api_key", optional: false, upstream: "main", inject: { in: "header", name: "X-Api-Key" } },
      { name: "personal", optional: true, upstream: "main", inject: { in: "header", name: "X-Personal" } },
    ],
  },
};

function setup({ alreadyInstalled = false } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  const curation = installCurationApi(api);
  let items = alreadyInstalled ? [installed] : [];
  const addSource = () => curation.sources.push({ ...source, active: false, enabled: false, inactive_reason: "curation_disabled" });
  if (alreadyInstalled) addSource();
  api.on("GET", base, () => jsonResponse(200, { items }));
  api.on("POST", base, () => {
    items = [installed];
    addSource();
    return sampleResponse("plugins/install");
  });
  api.on("DELETE", deletePath, () => {
    items = [];
    curation.sources = curation.sources.filter(({ id }) => id !== source.id);
    return new Response(null, { status: loadApiSample("plugins/delete").status });
  });
  return { api, curation, user: userEvent.setup() };
}

function mockPreview(result: ApiResult) {
  const requests: { method: string; path: string; body: FormData }[] = [];
  // 保留真实上传客户端与解码器，只把 XMLHttpRequest 的网络响应换成受控数据。
  class PreviewRequest extends EventTarget {
    method = "";
    path = "";
    status = result.status;
    responseText = JSON.stringify(result.body);

    open(method: string, path: string) {
      this.method = method;
      this.path = path;
    }

    setRequestHeader() {}

    getResponseHeader() {
      return "application/json";
    }

    send(body: FormData) {
      requests.push({ method: this.method, path: this.path, body });
      queueMicrotask(() => this.dispatchEvent(new Event("load")));
    }
  }
  vi.stubGlobal("XMLHttpRequest", PreviewRequest);
  return requests;
}

const pluginsCard = () => screen.findByRole("region", { name: "插件" });
const sourcesCard = () => screen.findByRole("region", { name: "外部源" });
const sourceSwitch = async () => within(await sourcesCard()).findByRole("switch", { name: `${name}（插件）` });

function descriptionOf(control: HTMLElement) {
  return document.getElementById(control.getAttribute("aria-describedby") ?? "")?.textContent;
}

async function upload(user: ReturnType<typeof userEvent.setup>, card: HTMLElement) {
  const file = new File(["mock zip"], "lyrics-demo.zip", { type: "application/zip" });
  const input = card.querySelector<HTMLInputElement>('input[type="file"]');
  if (input === null) throw new Error("找不到插件文件输入框");
  await user.upload(input, file);
  return file;
}

async function openDelete(user: ReturnType<typeof userEvent.setup>) {
  await user.click(within(await pluginsCard()).getByRole("button", { name: `${name} 的更多操作` }));
  await user.click(await screen.findByRole("menuitem", { name: `删除 ${name}` }));
  return within(await screen.findByRole("dialog", { name: deleteTitle }));
}

it("上传 ZIP 预检并展示安装确认信息，确认后两个列表刷新且新来源默认关闭", async () => {
  const { api, user } = setup();
  const requests = mockPreview({ status: 200, body: previewWithKeys });
  renderApp({ path: "/settings/curation" });

  const card = within(await pluginsCard());
  expect(await card.findByText(empty)).toBeTruthy();
  const sources = within(await sourcesCard());
  expect(await sources.findByRole("switch", { name: "MusicBrainz" })).toBeTruthy();
  expect(sources.queryByRole("switch", { name: `${name}（插件）` })).toBeNull();

  const file = await upload(user, await pluginsCard());
  const dialog = within(await screen.findByRole("dialog", { name: installTitle }));
  expect(requests).toMatchObject([{ method: "POST", path: `${base}/preview` }]);
  const form = requests[0]?.body;
  expect(form).toBeInstanceOf(FormData);
  expect(Array.from(form?.entries() ?? [])).toEqual([["file", file]]);
  expect(api.requestsTo("POST", base)).toHaveLength(0);

  for (const text of ["ROOMusic · 1.0.0", "歌词、艺人简介", upstream, "api_key、personal（可选）", "cf813b55b3a5"]) {
    expect(dialog.getByText(text, { exact: true })).toBeTruthy();
  }
  expect(dialog.queryByText(preview.sha256, { exact: true })).toBeNull();

  await user.click(dialog.getByRole("button", { name: "安装" }));
  expect(await card.findByText(name, { exact: true })).toBeTruthy();
  const toggle = await sourceSwitch();
  expect(toggle).toHaveProperty("checked", false);
  expect(descriptionOf(toggle)).toContain(`访问 ${upstream}`);
  await waitFor(() => expect(screen.queryByRole("dialog", { name: installTitle })).toBeNull());
  expect(api.requestsTo("POST", base).map(({ body }) => body)).toEqual([{ preview_token: preview.preview_token }]);
});

it("取消预检确认后不安装，同一个 ZIP 可以再次预检", async () => {
  const { api, user } = setup();
  const requests = mockPreview(loadApiSample("plugins/preview"));
  renderApp({ path: "/settings/curation" });

  const card = await pluginsCard();
  const file = await upload(user, card);
  const dialog = await screen.findByRole("dialog", { name: installTitle });
  await user.click(within(dialog).getByRole("button", { name: "取消" }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: installTitle })).toBeNull());
  expect(within(card).getByText(empty)).toBeTruthy();
  expect(api.requestsTo("POST", base)).toHaveLength(0);

  const input = card.querySelector<HTMLInputElement>('input[type="file"]');
  if (input === null) throw new Error("找不到插件文件输入框");
  await user.upload(input, file);
  expect(await screen.findByRole("dialog", { name: installTitle })).toBeTruthy();
  expect(requests).toHaveLength(2);
});

it("预检返回 invalid_plugin 时显示包格式提示且不打开确认框", async () => {
  const { api, user } = setup();
  mockPreview(failure(400, "invalid_plugin", "插件包或预检信息无效"));
  renderApp({ path: "/settings/curation" });

  const card = await pluginsCard();
  await upload(user, card);
  const error = await within(card).findByRole("alert");
  expect(error.textContent).toContain("插件没有通过检查");
  expect(error.textContent).toContain("需要只含 manifest.json 与 plugin.wasm 的 zip");
  expect(screen.queryByRole("dialog", { name: installTitle })).toBeNull();
  expect(api.requestsTo("POST", base)).toHaveLength(0);
});

it("列表中 hash_changed 的插件显示需要重新确认", async () => {
  const { api } = setup();
  api.on("GET", base, () => sampleResponse("plugins/hash-changed"));
  renderApp({ path: "/settings/curation" });

  const card = within(await pluginsCard());
  expect(await card.findByText("需要重新确认")).toBeTruthy();
  expect(card.getByText(/插件文件被改动过，已停用.*重新上传确认后才能再用/)).toBeTruthy();
  expect(card.queryByText("已启用")).toBeNull();
});

it("取消删除保留插件，确认删除后插件和外部源同时消失", async () => {
  const { api, user } = setup({ alreadyInstalled: true });
  renderApp({ path: "/settings/curation" });

  expect(await sourceSwitch()).toBeTruthy();
  const dialog = await openDelete(user);
  expect(dialog.getByText(/已经补上的内容不会自动去掉/)).toBeTruthy();
  await user.click(dialog.getByRole("button", { name: "取消" }));
  expect(api.requestsTo("DELETE", deletePath)).toHaveLength(0);
  expect(within(await pluginsCard()).getByText(name, { exact: true })).toBeTruthy();

  const confirming = await openDelete(user);
  await user.click(confirming.getByRole("button", { name: "删除" }));
  expect(await within(await pluginsCard()).findByText(empty)).toBeTruthy();
  const sources = within(await sourcesCard());
  await waitFor(() => expect(sources.queryByRole("switch", { name: `${name}（插件）` })).toBeNull());
  expect(api.requestsTo("DELETE", deletePath)).toHaveLength(1);
});

it("打开自动整理前，用插件的名称和访问地址说明发给它的数据", async () => {
  const { curation, user } = setup();
  curation.sources.push({ ...source, active: false, inactive_reason: "curation_disabled" });
  renderApp({ path: "/settings/curation" });

  const toggle = await sourceSwitch();
  expect(toggle).toHaveProperty("checked", true);
  expect(descriptionOf(toggle)).toContain(`访问 ${upstream}`);
  await user.click(await screen.findByRole("switch", { name: "自动整理" }));
  const dialog = within(await screen.findByRole("dialog", { name: "打开自动整理？" }));
  expect(dialog.getByText(name, { exact: true }).closest("li")?.textContent).toContain(`插件决定的查询内容，只会发往 ${upstream}`);
});

it("从外部源启用插件后，插件卡片也应显示已启用", async () => {
  const { api, user } = setup({ alreadyInstalled: true });
  let item = installed;
  api.on("GET", base, () => jsonResponse(200, { items: [item] }));
  api.on("PUT", sourcePath, () => {
    item = { ...item, enabled: true };
    return jsonResponse(200, { ...source, active: false, inactive_reason: "curation_disabled" });
  });
  renderApp({ path: "/settings/curation" });

  const card = within(await pluginsCard());
  expect(await card.findByText(name, { exact: true })).toBeTruthy();
  const toggle = await sourceSwitch();
  expect(toggle).toHaveProperty("checked", false);
  await user.click(toggle);
  await waitFor(() => expect(toggle).toHaveProperty("checked", true));
  expect(api.requestsTo("PUT", sourcePath).map(({ body }) => body)).toEqual([{ enabled: true }]);
  await waitFor(
    () => expect(card.queryByText("已启用"), "外部源开关已保存，插件卡片应同步").not.toBeNull(),
    { onTimeout: (error) => error },
  );
});

it("外部源里的可选插件密钥不应标为 ROOMusic 内置", async () => {
  const { curation } = setup();
  curation.sources.push(enabledSourceWithOptionalKey);
  renderApp({ path: "/settings/curation" });

  const key = await within(await sourcesCard()).findByRole("button", { name: `填写 ${name} 的personal` });
  expect(descriptionOf(key) ?? "", "可选插件密钥由管理员自行配置").not.toContain("ROOMusic 已内置");
});

it("打开自动整理的确认框不应把可选插件密钥写成请求的前置条件", async () => {
  const { curation, user } = setup();
  curation.sources.push(enabledSourceWithOptionalKey);
  renderApp({ path: "/settings/curation" });

  expect(await sourceSwitch()).toHaveProperty("checked", true);
  await user.click(await screen.findByRole("switch", { name: "自动整理" }));
  const dialog = within(await screen.findByRole("dialog", { name: "打开自动整理？" }));
  const disclosure = dialog.getByText(name, { exact: true }).closest("li")?.textContent ?? "";
  expect(disclosure).toContain(upstream);
  expect(disclosure).not.toContain("填入密钥后");
});
