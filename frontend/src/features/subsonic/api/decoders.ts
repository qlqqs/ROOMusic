import { decode as d } from "../../../shared/api";

// Subsonic 兼容接口的网页端设置（合同见 .trellis/spec/backend/subsonic-contracts.md）。纯类型与解码器。

/**
 * 本人的连接状态：ready 表示服务器存有能解密的登录密码副本，令牌方式可用。
 * jukebox_zone_id 是点唱机控制的播放区；没选过、或选的播放区已不存在时为 null，服务器按最近播放过的播放区处理。
 */
const connectionDecoder = d.object({
  enabled: d.boolean(),
  ready: d.boolean(),
  username: d.string({ min: 1, max: 256 }),
  jukebox_zone_id: d.nullable(d.uuid()),
});
export type SubsonicConnectionDTO = d.Infer<typeof connectionDecoder>;
export const decodeSubsonicConnection = d.defineDecoder(connectionDecoder);

/** 实例开关（管理员）。 */
const settingsDecoder = d.object({ enabled: d.boolean() });
export type SubsonicSettingsDTO = d.Infer<typeof settingsDecoder>;
export const decodeSubsonicSettings = d.defineDecoder(settingsDecoder);
