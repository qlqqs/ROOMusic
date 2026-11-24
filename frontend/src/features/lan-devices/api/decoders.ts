import { decode as d } from "../../../shared/api";

// 局域网设备（UPnP/DLNA 渲染器与 Google Cast）的管理员接口，合同见 .trellis/spec/backend/upnp-device-contracts.md。纯类型与解码器。

/**
 * 局域网设备的实例开关：enabled 是发现与使用（总开关，默认关），shared 是账号间共享（默认开；关时按设备分配账号），
 * upnp_enabled、cast_enabled 是两种协议各自的开关（默认开，只在总开关打开时起作用）。
 */
export const lanDeviceSettingsDecoder = d.object({ enabled: d.boolean(), shared: d.boolean(), upnp_enabled: d.boolean(), cast_enabled: d.boolean() });
export type LanDeviceSettingsDTO = d.Infer<typeof lanDeviceSettingsDecoder>;

/** Google Cast 设备的编码设置（管理员按设备设定）：编码与采样率上限；FLAC 最高 96 kHz，其余 48 kHz。 */
export const castFormatDecoder = d.object({ codec: d.oneOf(["mp3", "aac", "flac"] as const), max_sample_rate: d.integer({ min: 1 }) });
export type CastFormatDTO = d.Infer<typeof castFormatDecoder>;

/** 一台发现或手动添加的设备。名称、型号来自设备自己的公告，按不可信文本显示。 */
export const lanDeviceDecoder = d.object({
  udn: d.string({ min: 1, max: 256 }),
  kind: d.oneOf(["upnp", "cast"] as const),
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
  /** 只有 Google Cast 设备有；UPnP 设备为 null。 */
  format: d.nullable(castFormatDecoder),
});
export type LanDeviceDTO = d.Infer<typeof lanDeviceDecoder>;

export const decodeLanDeviceSettings = d.defineDecoder(lanDeviceSettingsDecoder);
export const decodeLanDevices = d.defineDecoder(d.object({ items: d.array(lanDeviceDecoder, { max: 1000 }) }));
export const decodeLanDevice = d.defineDecoder(lanDeviceDecoder);
export const decodeCastFormat = d.defineDecoder(castFormatDecoder);
export const decodeLanDeviceGrants = d.defineDecoder(d.object({ user_ids: d.array(d.uuid(), { max: 10_000 }) }));
