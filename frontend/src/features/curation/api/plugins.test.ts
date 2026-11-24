import { expect, it } from "vitest";
import { decode as d, failureOf } from "../../../shared/api";
import { loadApiSample, sampleBody } from "../../../test/api-samples";
import { jsonResponse, stubFetch } from "../../../test/http";
import { withValue, without } from "../../../test/mutate";
import { decodeCurationSources } from "./decoders";
import { decodeInstalledPlugin, decodeInstalledPlugins, decodePluginPreview, deletePlugin, fetchPlugins } from "./plugins";

it("预检保留令牌、清单与完整指纹，拒绝缺少令牌、无效指纹和未知能力", () => {
  const sample = sampleBody("plugins/preview");
  expect(decodePluginPreview(sample)).toStrictEqual(sample);
  for (const input of [
    without(sample, ["preview_token"]),
    withValue(sample, ["sha256"], "cf813b55b3a5"),
    withValue(sample, ["capabilities", 0], "release_lookup"),
    withValue(sample, ["manifest", "upstreams", 0, "base_url"], null),
  ]) expect(() => decodePluginPreview(input)).toThrow(d.DecodeError);
});

it("安装保留默认停用状态，拒绝无效的状态和安装时间", () => {
  const sample = sampleBody("plugins/install");
  expect(decodeInstalledPlugin(sample)).toStrictEqual(sample);
  expect(decodeInstalledPlugin(sample).enabled).toBe(false);
  for (const input of [withValue(sample, ["enabled"], "false"), without(sample, ["installed_at"])]) {
    expect(() => decodeInstalledPlugin(input)).toThrow(d.DecodeError);
  }
});

it("列表接受空列表与缺省、空值、hash_changed 停用原因，拒绝不合法的原因", () => {
  const list = sampleBody("plugins/list");
  const changed = sampleBody("plugins/hash-changed");
  const explicitlyNull = withValue(changed, ["items", 0, "disabled_reason"], null);
  for (const input of [{ items: [] }, list, changed, explicitlyNull]) {
    expect(decodeInstalledPlugins(input)).toStrictEqual(input);
  }
  expect(decodeInstalledPlugins(changed).items[0]?.disabled_reason).toBe("hash_changed");
  expect(() => decodeInstalledPlugins(withValue(changed, ["items", 0, "disabled_reason"], false))).toThrow(d.DecodeError);
});

it("删除接口将无响应体的 204 解码为 null", async () => {
  const sample = loadApiSample("plugins/delete");
  const requests = stubFetch(() => new Response(null, { status: sample.status }));
  await expect(deletePlugin("lyrics-demo")).resolves.toEqual({ status: 204, data: sample.body });
  expect(requests).toMatchObject([{ method: "DELETE", path: "/api/v1/admin/plugins/lyrics-demo" }]);
});

it("插件列表返回不合法的成功响应时，客户端报告 invalid_response", async () => {
  stubFetch(() => jsonResponse(200, { items: null }));
  const error = await fetchPlugins().catch((reason: unknown) => reason);
  expect(failureOf(error)).toMatchObject({ kind: "invalid_response", status: 200 });
});

it("外部源列表保留 plugin:<id> 与 upstreams，拒绝无效的上游条目", () => {
  const source = sampleBody("plugins/enable-source");
  const builtIn = decodeCurationSources(sampleBody("curation/sources"));
  const list = { items: [...builtIn.items, source] };
  const decoded = decodeCurationSources(list);
  expect(decoded).toStrictEqual(list);
  expect(decoded.items.find(({ id }) => id === "plugin:lyrics-demo")?.upstreams).toEqual(["https://lyrics.example.org/api"]);
  expect(() => decodeCurationSources({ items: [withValue(source, ["upstreams", 0], 42)] })).toThrow(d.DecodeError);
});
