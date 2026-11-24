import { decodeLanDevice, decodeLanDeviceSettings } from "../../src/features/lan-devices/api/decoders";
import * as d from "../../src/shared/api/decode";
import { loadApiSample, sampleBody } from "../../src/test/api-samples";

export const LAN_DEVICE_API = "/api/v1/admin/upnp";
export const manualRenderer = decodeLanDevice(sampleBody("upnp/manual-added"));
export const rendererIp = new URL(manualRenderer.address).hostname;

const settingsPatch = d.defineDecoder(d.object({ enabled: d.optional(d.boolean()), shared: d.optional(d.boolean()), upnp_enabled: d.optional(d.boolean()), cast_enabled: d.optional(d.boolean()), airplay_enabled: d.optional(d.boolean()) }));
const addressBody = d.defineDecoder(d.object({ address: d.string({ min: 1 }) }));
const grantsBody = d.defineDecoder(d.object({ user_ids: d.array(d.uuid(), { max: 10_000 }) }));

export function lanDevicesMock() {
  let settings = decodeLanDeviceSettings(sampleBody("upnp/settings"));
  const devices = new Map<string, typeof manualRenderer>();

  return {
    respond(method: string, path: string, body: unknown): { status: number; body: unknown } {
      if (path === `${LAN_DEVICE_API}/settings`) {
        if (method === "PUT") settings = { ...settings, ...settingsPatch(body) };
        if (method === "GET" || method === "PUT") return { status: 200, body: settings };
      }
      if (path === `${LAN_DEVICE_API}/devices`) {
        if (method === "GET") return { status: 200, body: { items: [...devices.values()] } };
        if (method === "POST") {
          if (!settings.enabled) return loadApiSample("upnp/disabled");
          const { address } = addressBody(body);
          if (address !== rendererIp && address !== manualRenderer.address) return loadApiSample("upnp/invalid-address");
          devices.set(manualRenderer.udn, manualRenderer);
          return { status: 200, body: manualRenderer };
        }
      }
      const grantsPath = `${LAN_DEVICE_API}/devices/${encodeURIComponent(manualRenderer.udn)}/grants`;
      const device = devices.get(manualRenderer.udn);
      if (path === grantsPath && method === "PUT" && device !== undefined) {
        const { user_ids } = grantsBody(body);
        devices.set(device.udn, { ...device, user_ids });
        return { status: 200, body: { user_ids } };
      }
      return loadApiSample("errors/not-found-api");
    },
  };
}
