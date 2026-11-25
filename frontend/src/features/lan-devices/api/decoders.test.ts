import { describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeAirPlayFormat, decodeCastFormat, decodeLanDevice, decodeLanDeviceGrants, decodeLanDevices, decodeLanDeviceSettings, decodeSlimProtoFormat } from "./decoders";

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
    airplay: null,
  },
  format: null,
};

describe("局域网设备管理接口解码", () => {
  it("读取和更新两个开关", () => {
    expect(decodeLanDeviceSettings(sampleBody("upnp/settings"))).toStrictEqual({ enabled: false, shared: true, upnp_enabled: true, cast_enabled: true, airplay_enabled: true, slimproto_enabled: true, slimproto_unavailable: null });
    expect(decodeLanDeviceSettings(sampleBody("upnp/settings-updated"))).toStrictEqual({ enabled: true, shared: false, upnp_enabled: true, cast_enabled: true, airplay_enabled: true, slimproto_enabled: true, slimproto_unavailable: null });
    expect(() => decodeLanDeviceSettings({ enabled: true })).toThrow("$.shared");
    expect(() => decodeLanDeviceSettings({ enabled: "true", shared: false, upnp_enabled: true, cast_enabled: true, airplay_enabled: true, slimproto_enabled: true, slimproto_unavailable: null })).toThrow("$.enabled");
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
    expect(() => decodeLanDevice({ ...device, kind: "bluetooth" })).toThrow("$.kind");
  });

  it("AirPlay 设备带公告信息与 ALAC 音质", () => {
    const airplay = decodeLanDevice(sampleBody("upnp/airplay-manual-added"));
    expect(airplay.kind).toBe("airplay");
    expect(airplay.format).toStrictEqual({ codec: "alac", max_sample_rate: 44100, max_bit_depth: 16 });
    expect(airplay.capabilities.airplay?.device_id).toBe("02:3F:BA:15:D9:79");
    expect(decodeAirPlayFormat(sampleBody("upnp/airplay-high-resolution-format"))).toStrictEqual({ codec: "alac", max_sample_rate: 48000, max_bit_depth: 24 });
    expect(() => decodeLanDevice({ ...airplay, format: { codec: "alac", max_sample_rate: 44100 } })).toThrow("$.format.max_bit_depth");
  });

  it("SlimProto 播放器行完整解码：播放器自报信息、FLAC 音质与跟随播放器的采样率", () => {
    expect(decodeLanDevices(sampleBody("upnp/slimproto-list"))).toStrictEqual({
      items: [
        {
          udn: "slim:02:00:00:00:00:01",
          kind: "slimproto",
          name: "书房",
          model: "squeezelite",
          address: "192.168.44.2",
          online: true,
          manual: false,
          user_ids: [],
          capabilities: {
            pause: true,
            volume: "number",
            stream_formats: ["flac"],
            format_declared: true,
            max_sample_rate: 192000,
            max_bit_depth: 24,
            airplay: null,
            slimproto: { model_name: "SqueezeLite", firmware: "2.0.0", declared_max_sample_rate: 192000, unsupported: false },
          },
          format: { codec: "flac", max_sample_rate: null, volume: "number" },
        },
      ],
    });
    expect(decodeSlimProtoFormat(sampleBody("upnp/slimproto-format"))).toStrictEqual({ codec: "flac", max_sample_rate: 96000, volume: "fixed" });
  });

  it("按设备种类解码格式：Cast 的 FLAC 不带音量方式，SlimProto 缺音量方式时报错", () => {
    const cast = decodeLanDevice({ ...sampleBody("upnp/cast-manual-added") as object, format: { codec: "flac", max_sample_rate: 96000 } });
    expect(cast.format).toStrictEqual({ codec: "flac", max_sample_rate: 96000 });
    expect(cast.format !== null && "volume" in cast.format).toBe(false);
    const [slim] = (sampleBody("upnp/slimproto-list") as { items: object[] }).items;
    expect(() => decodeLanDevice({ ...slim, format: { codec: "flac", max_sample_rate: null } })).toThrow("$.format.volume");
    expect(() => decodeLanDevice({ ...slim, format: { codec: "flac", max_sample_rate: null, volume: "loud" } })).toThrow("$.format.volume");
    expect(() => decodeLanDevice({ ...slim, format: { codec: "mp3", max_sample_rate: 48000, volume: "number" } })).toThrow("$.format.codec");
  });

  it("非 SlimProto 设备不带 slimproto 项也能解码，带 null 时保留", () => {
    expect("slimproto" in decodeLanDevice(device).capabilities).toBe(false);
    expect(decodeLanDevice({ ...device, capabilities: { ...device.capabilities, slimproto: null } }).capabilities.slimproto).toBeNull();
  });

  it("端口 3483 被占用时设置带只读原因码", () => {
    expect(decodeLanDeviceSettings(sampleBody("upnp/slimproto-port-in-use"))).toStrictEqual({
      enabled: true,
      shared: true,
      upnp_enabled: false,
      cast_enabled: false,
      airplay_enabled: false,
      slimproto_enabled: true,
      slimproto_unavailable: "port_in_use",
    });
    expect(() => decodeLanDeviceSettings({ ...sampleBody("upnp/slimproto-port-in-use") as object, slimproto_unavailable: "" })).toThrow("$.slimproto_unavailable");
  });

  it("分配返回成员 ID，允许清空并拒绝无效的成员 ID", () => {
    expect(decodeLanDeviceGrants(sampleBody("upnp/grants-updated"))).toStrictEqual({
      user_ids: ["00000000-0000-4000-8000-000000000001"],
    });
    expect(decodeLanDeviceGrants({ user_ids: [] })).toStrictEqual({ user_ids: [] });
    expect(() => decodeLanDeviceGrants({ user_ids: ["listener"] })).toThrow("$.user_ids[0]");
  });
});
