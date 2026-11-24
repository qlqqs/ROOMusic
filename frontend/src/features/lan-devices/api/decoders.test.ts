import { describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeCastFormat, decodeLanDevice, decodeLanDeviceGrants, decodeLanDevices, decodeLanDeviceSettings } from "./decoders";

const udn = "uuid:00000000-0000-4000-8000-000000000001";
const device = {
  udn,
  kind: "upnp",
  name: "客厅音响",
  model: "Fake Renderer",
  address: "http://192.168.44.2:49152/description.xml",
  online: true,
  manual: true,
  user_ids: [],
  capabilities: {
    pause: true,
    volume: "number",
    stream_formats: ["flac"],
    format_declared: true,
    max_sample_rate: 48000,
    max_bit_depth: 24,
  },
  format: null,
};

describe("局域网设备管理接口解码", () => {
  it("读取和更新两个开关", () => {
    expect(decodeLanDeviceSettings(sampleBody("upnp/settings"))).toStrictEqual({ enabled: false, shared: true, upnp_enabled: true, cast_enabled: true });
    expect(decodeLanDeviceSettings(sampleBody("upnp/settings-updated"))).toStrictEqual({ enabled: true, shared: false, upnp_enabled: true, cast_enabled: true });
    expect(() => decodeLanDeviceSettings({ enabled: true })).toThrow("$.shared");
    expect(() => decodeLanDeviceSettings({ enabled: "true", shared: false, upnp_enabled: true, cast_enabled: true })).toThrow("$.enabled");
  });

  it("保留设备信息、能力和成员分配，也接受空列表", () => {
    expect(decodeLanDevices(sampleBody("upnp/list"))).toStrictEqual({
      items: [{ ...device, user_ids: ["00000000-0000-4000-8000-000000000002"] }],
    });
    expect(decodeLanDevices(sampleBody("upnp/list-empty"))).toStrictEqual({ items: [] });
    expect(() => decodeLanDevices({ items: [{ ...device, capabilities: { ...device.capabilities, volume: "unknown" } }] })).toThrow("$.items[0].capabilities.volume");
  });

  it("手动添加返回设备行，并拒绝缺少设备标识的响应", () => {
    expect(decodeLanDevice(sampleBody("upnp/manual-added"))).toStrictEqual(device);
    expect(() => decodeLanDevice({ ...device, udn: undefined })).toThrow("$.udn");
  });

  it("Google Cast 设备带编码设置，只接受约定的编码", () => {
    const cast = decodeLanDevice(sampleBody("upnp/cast-manual-added"));
    expect(cast.kind).toBe("cast");
    expect(cast.format).toStrictEqual({ codec: "mp3", max_sample_rate: 48000 });
    expect(decodeCastFormat(sampleBody("upnp/cast-format"))).toStrictEqual({ codec: "flac", max_sample_rate: 96000 });
    expect(() => decodeLanDevice({ ...cast, format: { codec: "wav", max_sample_rate: 48000 } })).toThrow("$.format.codec");
    expect(() => decodeLanDevice({ ...device, kind: "airplay" })).toThrow("$.kind");
  });

  it("分配返回成员 ID，允许清空并拒绝无效的成员 ID", () => {
    expect(decodeLanDeviceGrants(sampleBody("upnp/grants-updated"))).toStrictEqual({
      user_ids: ["00000000-0000-4000-8000-000000000001"],
    });
    expect(decodeLanDeviceGrants({ user_ids: [] })).toStrictEqual({ user_ids: [] });
    expect(() => decodeLanDeviceGrants({ user_ids: ["listener"] })).toThrow("$.user_ids[0]");
  });
});
