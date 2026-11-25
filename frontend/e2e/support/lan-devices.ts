import { decodeLanDevice, decodeLanDevices, decodeLanDeviceSettings, decodeSlimProtoFormat } from "../../src/features/lan-devices/api/decoders";
import * as d from "../../src/shared/api/decode";
import { loadApiSample, sampleBody } from "../../src/test/api-samples";

export const LAN_DEVICE_API = "/api/v1/admin/upnp";
export const manualRenderer = decodeLanDevice(sampleBody("upnp/manual-added"));
export const rendererIp = new URL(manualRenderer.address).hostname;

/** slimproto-list 样本里的 SlimProto 播放器：自己连上来的设备，声明最高 192 kHz。 */
const [slimProtoSample] = decodeLanDevices(sampleBody("upnp/slimproto-list")).items;
if (slimProtoSample === undefined) throw new Error("slimproto-list 样本缺少播放器");
export const slimProtoPlayer = slimProtoSample;

type LanDevice = typeof manualRenderer;

const settingsPatch = d.defineDecoder(d.object({
  enabled: d.optional(d.boolean()),
  shared: d.optional(d.boolean()),
  upnp_enabled: d.optional(d.boolean()),
  cast_enabled: d.optional(d.boolean()),
  airplay_enabled: d.optional(d.boolean()),
  slimproto_enabled: d.optional(d.boolean()),
}));
const addressBody = d.defineDecoder(d.object({ address: d.string({ min: 1 }) }));
const grantsBody = d.defineDecoder(d.object({ user_ids: d.array(d.uuid(), { max: 10_000 }) }));

export function lanDevicesMock() {
  let settings = decodeLanDeviceSettings(sampleBody("upnp/settings"));
  const devices = new Map<string, LanDevice>();

  /** 保存 SlimProto 音质：设备行的格式换成保存值，采样率上限取它与播放器声明值的较小者，音量方式立即生效。 */
  function saveSlimProtoFormat(device: LanDevice, body: unknown): { status: number; body: unknown } {
    const format = decodeSlimProtoFormat(body);
    const declared = device.capabilities.slimproto?.declared_max_sample_rate ?? device.capabilities.max_sample_rate;
    const max_sample_rate = Math.min(format.max_sample_rate ?? declared, declared);
    devices.set(device.udn, { ...device, format, capabilities: { ...device.capabilities, max_sample_rate, volume: format.volume } });
    return { status: 200, body: format };
  }

  return {
    /** 端口 3483 被占用等只读原因；null 表示 SlimProto 可用。 */
    setSlimProtoUnavailable(reason: string | null) {
      settings = { ...settings, slimproto_unavailable: reason };
    },
    /** 放入 slimproto-list 样本里的播放器（模拟它已经连上来）。 */
    addSlimProtoPlayer() {
      devices.set(slimProtoPlayer.udn, slimProtoPlayer);
      return slimProtoPlayer;
    },
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
      const slimProto = devices.get(slimProtoPlayer.udn);
      if (path === `${LAN_DEVICE_API}/devices/${encodeURIComponent(slimProtoPlayer.udn)}/format` && method === "PUT" && slimProto !== undefined) {
        return saveSlimProtoFormat(slimProto, body);
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
