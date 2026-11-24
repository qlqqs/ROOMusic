import { decodeCurationSource } from "../../src/features/curation/api/decoders";
import { decodeInstalledPlugin, decodePluginPreview, type InstalledPluginDTO } from "../../src/features/curation/api/plugins";
import { loadApiSample, sampleBody } from "../../src/test/api-samples";
import { failure } from "../../src/test/catalog-data";
import { refreshSources, type CurationData } from "../../src/test/curation-data";

export const PLUGINS_API = "/api/v1/admin/plugins";
const preview = decodePluginPreview(sampleBody("plugins/preview"));
const installed = decodeInstalledPlugin(sampleBody("plugins/install"));
const source = decodeCurationSource(sampleBody("plugins/enable-source"));

export const demoPlugin = { id: installed.manifest.id, name: installed.manifest.name, fileName: "lyrics-demo.zip", sourceId: source.id, token: preview.preview_token };

export function pluginsMock() {
  let item: InstalledPluginDTO | null = null;
  let pending = false;

  return {
    list(curation: CurationData) {
      const enabled = curation.sources.find(({ id }) => id === source.id)?.enabled === true;
      return { status: 200, body: { items: item === null ? [] : [{ ...item, enabled }] } };
    },
    preview(contentType: string | undefined, body: unknown) {
      const boundary = /^multipart\/form-data;\s*boundary=([^\s;]+)$/i.exec(contentType ?? "")?.[1];
      if (boundary === undefined || typeof body !== "string" || !body.startsWith(
        `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${demoPlugin.fileName}"\r\n`,
      )) return failure(400, "invalid_plugin", "插件包或预检信息无效");
      pending = true;
      return loadApiSample("plugins/preview");
    },
    install(curation: CurationData, body: unknown) {
      if (!pending || typeof body !== "object" || body === null || !("preview_token" in body) || body.preview_token !== preview.preview_token) {
        return failure(400, "invalid_plugin", "插件包或预检信息无效");
      }
      pending = false;
      item = installed;
      curation.sources = [...curation.sources.filter(({ id }) => id !== source.id), { ...source, enabled: item.enabled }];
      refreshSources(curation);
      return loadApiSample("plugins/install");
    },
    delete(curation: CurationData) {
      if (item === null) return failure(404, "not_found", "插件不存在");
      item = null;
      curation.sources = curation.sources.filter(({ id }) => id !== source.id);
      return loadApiSample("plugins/delete");
    },
  };
}
