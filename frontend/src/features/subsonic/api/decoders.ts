import { decode as d } from "../../../shared/api";

// Subsonic 兼容接口的网页端设置（合同见 .trellis/spec/backend/subsonic-contracts.md）。纯类型与解码器。

/** 本人的连接状态：ready 表示服务器存有能解密的登录密码副本，令牌方式可用。 */
const connectionDecoder = d.object({
  enabled: d.boolean(),
  ready: d.boolean(),
  username: d.string({ min: 1, max: 256 }),
});
export type SubsonicConnectionDTO = d.Infer<typeof connectionDecoder>;
export const decodeSubsonicConnection = d.defineDecoder(connectionDecoder);

/** 实例开关（管理员）。 */
const settingsDecoder = d.object({ enabled: d.boolean() });
export type SubsonicSettingsDTO = d.Infer<typeof settingsDecoder>;
export const decodeSubsonicSettings = d.defineDecoder(settingsDecoder);
