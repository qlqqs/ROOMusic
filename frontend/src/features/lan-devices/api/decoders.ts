import { decode as d } from "../../../shared/api";

// 局域网 UPnP 渲染器的管理员接口（M8 U3，合同见 .trellis/spec/backend/upnp-device-contracts.md）。纯类型与解码器。

/** 局域网设备的两个实例开关：enabled 是发现与使用（默认关），shared 是账号间共享（默认开；关时按设备分配账号）。 */
export const lanDeviceSettingsDecoder = d.object({ enabled: d.boolean(), shared: d.boolean() });
export type LanDeviceSettingsDTO = d.Infer<typeof lanDeviceSettingsDecoder>;

/** 一台发现或手动添加的渲染器。名称、型号来自设备自己的描述文件，按不可信文本显示。 */
export const lanDeviceDecoder = d.object({
  udn: d.string({ min: 1, max: 256 }),
  name: d.string({ max: 256 }),
  model: d.string({ max: 256 }),
  address: d.string({ min: 1, max: 2048 }),
  online: d.boolean(),
  manual: d.boolean(),
  user_ids: d.array(d.uuid(), { max: 10_000 }),
  capabilities: d.object({
    pause: d.boolean(),
    volume: d.oneOf(["number", "fixed"] as const),
    stream_formats: d.array(d.string({ min: 1, max: 32 }), { max: 8 }),
    format_declared: d.boolean(),
    max_sample_rate: d.integer({ min: 1 }),
    max_bit_depth: d.integer({ min: 1 }),
  }),
});
export type LanDeviceDTO = d.Infer<typeof lanDeviceDecoder>;

export const decodeLanDeviceSettings = d.defineDecoder(lanDeviceSettingsDecoder);
export const decodeLanDevices = d.defineDecoder(d.object({ items: d.array(lanDeviceDecoder, { max: 1000 }) }));
export const decodeLanDevice = d.defineDecoder(lanDeviceDecoder);
export const decodeLanDeviceGrants = d.defineDecoder(d.object({ user_ids: d.array(d.uuid(), { max: 10_000 }) }));
