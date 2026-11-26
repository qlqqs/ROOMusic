import { decode as d } from "../../../shared/api";

// 全局出站代理的管理员接口（合同见 .trellis/tasks/11-26-outbound-proxy/design.md 与后端出站合同）。纯类型与解码器。

export const PROXY_SCHEMES = ["http", "socks5"] as const;
export type ProxyScheme = (typeof PROXY_SCHEMES)[number];

export const PROXY_TEST_CODES = ["proxy_unreachable", "proxy_auth_failed", "target_unreachable", "timeout"] as const;
export type ProxyTestCode = (typeof PROXY_TEST_CODES)[number];

/** 已保存的代理：密码只回“有没有设置”。没填过主机时 host 为空串、port 为 null。 */
const proxyDecoder = d.object({
  enabled: d.boolean(),
  scheme: d.oneOf(PROXY_SCHEMES),
  host: d.string({ max: 255 }),
  port: d.nullable(d.integer({ min: 1, max: 65535 })),
  username: d.string({ max: 255 }),
  password_set: d.boolean(),
  version: d.integer({ min: 0 }),
});
export type ProxySettingsDTO = d.Infer<typeof proxyDecoder>;
export const decodeProxySettings = d.defineDecoder(proxyDecoder);

/** 测试连接的结果：成功时 code 为 null；失败时 latency_ms 可能为空。 */
const testDecoder = d.object({
  ok: d.boolean(),
  code: d.nullable(d.oneOf(PROXY_TEST_CODES)),
  latency_ms: d.nullable(d.integer({ min: 0 })),
});
export type ProxyTestDTO = d.Infer<typeof testDecoder>;
export const decodeProxyTest = d.defineDecoder(testDecoder);

/** 保存与测试共用的草稿。password 省略表示沿用已存的，空串表示清除（只用于保存）。 */
export type ProxyDraft = {
  enabled: boolean;
  scheme: ProxyScheme;
  host: string;
  port: number | null;
  username: string;
  password?: string;
};
