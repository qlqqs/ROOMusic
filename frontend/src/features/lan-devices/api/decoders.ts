import { decode as d } from "../../../shared/api";

// 局域网设备（UPnP/DLNA 渲染器、Google Cast、AirPlay 与 SlimProto 播放器）的管理员接口，合同见 .trellis/spec/backend/upnp-device-contracts.md。纯类型与解码器。

/**
 * 局域网设备的实例开关：enabled 是发现与使用（总开关，默认关），shared 是账号间共享（默认开；关时按设备分配账号），
 * upnp_enabled、cast_enabled、airplay_enabled、slimproto_enabled 是各协议的开关（默认开，只在总开关打开时起作用）。
 * slimproto_unavailable 只读：SlimProto 打开但端口 3483 被本机其他程序占用时为 "port_in_use"，否则为 null。
 */
export const lanDeviceSettingsDecoder = d.object({
  enabled: d.boolean(),
  shared: d.boolean(),
  upnp_enabled: d.boolean(),
  cast_enabled: d.boolean(),
  airplay_enabled: d.boolean(),
  slimproto_enabled: d.boolean(),
  slimproto_unavailable: d.nullable(d.string({ min: 1, max: 64 })),
});
export type LanDeviceSettingsDTO = d.Infer<typeof lanDeviceSettingsDecoder>;
/** 可写的开关：只读的 slimproto_unavailable 不随请求发送。 */
export type LanDeviceSettingsChange = Partial<Omit<LanDeviceSettingsDTO, "slimproto_unavailable">>;

/** Google Cast 设备的编码设置（管理员按设备设定）：编码与采样率上限；FLAC 最高 96 kHz，其余 48 kHz。 */
export const castFormatDecoder = d.object({ codec: d.oneOf(["mp3", "aac", "flac"] as const), max_sample_rate: d.integer({ min: 1 }) });
export type CastFormatDTO = d.Infer<typeof castFormatDecoder>;

/** AirPlay 设备的音质（管理员按设备设定）：ALAC，44.1 kHz/16 位，AirPlay 2 设备另可 48 kHz/24 位。 */
export const airPlayFormatDecoder = d.object({ codec: d.literal("alac"), max_sample_rate: d.integer({ min: 1 }), max_bit_depth: d.integer({ min: 1 }) });
export type AirPlayFormatDTO = d.Infer<typeof airPlayFormatDecoder>;

/** SlimProto 播放器的音质（管理员按设备设定）：FLAC；采样率上限为 null 时跟随播放器声明；音量可调或固定。 */
export const slimProtoFormatDecoder = d.object({ codec: d.literal("flac"), max_sample_rate: d.nullable(d.integer({ min: 1 })), volume: d.oneOf(["number", "fixed"] as const) });
export type SlimProtoFormatDTO = d.Infer<typeof slimProtoFormatDecoder>;

/** SlimProto 播放器自报的信息。名称与固件来自播放器，按不可信文本显示。 */
export const slimProtoInfoDecoder = d.object({
  model_name: d.string({ max: 256 }),
  firmware: d.string({ max: 256 }),
  declared_max_sample_rate: d.integer({ min: 1 }),
  unsupported: d.boolean(),
});
export type SlimProtoInfoDTO = d.Infer<typeof slimProtoInfoDecoder>;

/** AirPlay 设备的公告与对时情况。名称等来自设备公告，按不可信文本显示；原因文字由服务器给出。 */
export const airPlayInfoDecoder = d.object({
  device_id: d.string({ min: 1, max: 64 }),
  features: d.string({ max: 32 }),
  flags: d.string({ max: 32 }),
  public_key: d.string({ max: 128 }),
  airplay_address: d.string({ max: 2048 }),
  raop_address: d.string({ max: 2048 }),
  raop_encryption: d.string({ max: 64 }),
  fairplay: d.boolean(),
  requires_pin: d.boolean(),
  requires_password: d.boolean(),
  paired: d.boolean(),
  ptp_required: d.boolean(),
  timing_protocol: d.nullable(d.string({ min: 1, max: 16 })),
  timing_reason: d.nullable(d.string({ min: 1, max: 200 })),
  unsupported: d.boolean(),
  unavailable_reason: d.nullable(d.string({ min: 1, max: 200 })),
});
export type AirPlayInfoDTO = d.Infer<typeof airPlayInfoDecoder>;

const lanDeviceBaseDecoder = d.object({
  udn: d.string({ min: 1, max: 256 }),
  kind: d.oneOf(["upnp", "cast", "airplay", "slimproto"] as const),
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
    /** 只有 AirPlay 设备有；其他设备为 null。 */
    airplay: d.nullable(airPlayInfoDecoder),
    /** 只有 SlimProto 播放器有；其他设备不带这一项。 */
    slimproto: d.optional(d.nullable(slimProtoInfoDecoder)),
  }),
});

/** Google Cast、AirPlay 与 SlimProto 设备有，由管理员设定；UPnP 设备为 null。Cast 与 SlimProto 都可能是 FLAC，所以按设备种类解码。 */
export type LanDeviceFormatDTO = CastFormatDTO | AirPlayFormatDTO | SlimProtoFormatDTO;
export type LanDeviceDTO = d.Infer<typeof lanDeviceBaseDecoder> & { format: LanDeviceFormatDTO | null };

const formatDecoders: Record<LanDeviceDTO["kind"], d.Decoder<LanDeviceFormatDTO>> = {
  upnp: castFormatDecoder,
  cast: castFormatDecoder,
  airplay: airPlayFormatDecoder,
  slimproto: slimProtoFormatDecoder,
};

/** 一台发现或手动添加的设备。名称、型号来自设备自己的公告，按不可信文本显示。 */
export const lanDeviceDecoder: d.Decoder<LanDeviceDTO> = (input, path) => {
  const device = lanDeviceBaseDecoder(input, path);
  const { format } = d.object({ format: d.nullable(formatDecoders[device.kind]) })(input, path);
  return { ...device, format };
};

export const decodeLanDeviceSettings = d.defineDecoder(lanDeviceSettingsDecoder);
export const decodeLanDevices = d.defineDecoder(d.object({ items: d.array(lanDeviceDecoder, { max: 1000 }) }));
export const decodeLanDevice = d.defineDecoder(lanDeviceDecoder);
export const decodeCastFormat = d.defineDecoder(castFormatDecoder);
export const decodeAirPlayFormat = d.defineDecoder(airPlayFormatDecoder);
export const decodeSlimProtoFormat = d.defineDecoder(slimProtoFormatDecoder);
export const decodeLanDeviceGrants = d.defineDecoder(d.object({ user_ids: d.array(d.uuid(), { max: 10_000 }) }));
