import type { ApiResponse } from "../../../shared/api";
import { apiRequest, apiUpload, decode as d } from "../../../shared/api";

// 第三方数据源插件的管理员接口（M8，ADR 0007；合同见 .trellis/spec/backend/plugin-contracts.md）。
// 清单里的名称、作者、地址都是插件作者写的不可信文本，只按纯文本显示。

export const PLUGIN_CAPABILITIES = ["cover_art", "lyrics", "artist_image", "artist_bio"] as const;

const manifestDecoder = d.object({
  abi: d.integer({ min: 1 }),
  id: d.string({ min: 3, max: 40, pattern: /^[a-z0-9-]+$/ }),
  name: d.string({ min: 1, max: 200 }),
  author: d.string({ max: 200 }),
  version: d.string({ min: 1, max: 64 }),
  upstreams: d.array(d.object({ id: d.string({ min: 1, max: 64 }), base_url: d.string({ min: 1, max: 2048 }), min_interval_ms: d.integer({ min: 0 }) }), { max: 16 }),
  keys: d.array(d.object({ name: d.string({ min: 1, max: 32 }), optional: d.optional(d.boolean()) }), { max: 8 }),
});

export const pluginPreviewDecoder = d.object({
  manifest: manifestDecoder,
  capabilities: d.array(d.oneOf(PLUGIN_CAPABILITIES), { max: PLUGIN_CAPABILITIES.length }),
  sha256: d.string({ min: 64, max: 64, pattern: /^[0-9a-f]+$/ }),
  preview_token: d.string({ min: 1, max: 256 }),
});
export type PluginPreviewDTO = d.Infer<typeof pluginPreviewDecoder>;

// disabled_reason：插件文件被改动（hash_changed）等原因自动停用；需要重新上传确认。
export const installedPluginDecoder = d.object({
  manifest: manifestDecoder,
  capabilities: d.array(d.oneOf(PLUGIN_CAPABILITIES), { max: PLUGIN_CAPABILITIES.length }),
  sha256: d.string({ min: 64, max: 64, pattern: /^[0-9a-f]+$/ }),
  enabled: d.boolean(),
  installed_at: d.timestamp(),
  disabled_reason: d.optional(d.nullable(d.string({ min: 1, max: 64 }))),
});
export type InstalledPluginDTO = d.Infer<typeof installedPluginDecoder>;

export const decodePluginPreview = d.defineDecoder(pluginPreviewDecoder);
export const decodeInstalledPlugin = d.defineDecoder(installedPluginDecoder);
export const decodeInstalledPlugins = d.defineDecoder(d.object({ items: d.array(installedPluginDecoder, { max: 200 }) }));

const BASE = "/api/v1/admin/plugins" as const;

/** 上传插件包（zip：manifest.json + plugin.wasm）做预检：返回清单、能力、要访问的地址与密钥、哈希与确认令牌；包不合格时 400 invalid_plugin。 */
export function previewPlugin(file: File, onProgress?: (fraction: number) => void): Promise<ApiResponse<PluginPreviewDTO>> {
  const form = new FormData();
  form.append("file", file);
  return apiUpload({ path: `${BASE}/preview`, form, ...(onProgress === undefined ? {} : { onProgress }) }, decodePluginPreview);
}

/** 确认安装预检过的包（默认不启用；启用在“外部源”里打开它的开关）。 */
export function installPlugin(previewToken: string): Promise<ApiResponse<InstalledPluginDTO>> {
  return apiRequest({ method: "POST", path: BASE, body: { preview_token: previewToken } }, decodeInstalledPlugin);
}

export function fetchPlugins(signal?: AbortSignal): Promise<ApiResponse<{ items: InstalledPluginDTO[] }>> {
  return apiRequest({ method: "GET", path: BASE, signal }, decodeInstalledPlugins);
}

/** 删除插件：停用并删除文件；它写入的内容不自动撤销（用“撤销来自它的全部自动修改”）。 */
export function deletePlugin(id: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `${BASE}/${encodeURIComponent(id)}` as const }, () => null);
}
