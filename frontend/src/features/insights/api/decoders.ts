import { decode as d } from "../../../shared/api";

// 匿名统计的管理员接口与公开的“关于”接口（合同见 .trellis/spec/backend/insights-contracts.md）。纯类型与解码器。

const libraryBucket = d.oneOf(["0", "1-999", "1k-10k", "10k-50k", "50k+"] as const);
const smallBucket = d.oneOf(["0", "1", "2-5", "6+"] as const);

/** 将要发送的心跳：字段与后端载荷一一对应，数字都是分档。 */
const payloadDecoder = d.object({
  schema: d.literal(3),
  instance_id: d.uuid(),
  version: d.string({ min: 1, max: 64 }),
  os: d.string({ min: 1, max: 32 }),
  arch: d.string({ min: 1, max: 32 }),
  container: d.boolean(),
  library: d.object({ tracks: libraryBucket, releases: libraryBucket }),
  members: d.oneOf(["1", "2-5", "6-20", "21+"] as const),
  features: d.object({
    auto_curation: d.boolean(),
    steward_model: d.boolean(),
    mcp_token: d.boolean(),
    lan_devices: d.boolean(),
    dsp: d.boolean(),
    scheduled_scan: d.boolean(),
    plugins: smallBucket,
  }),
  devices_30d: d.object({ browser: smallBucket, upnp: smallBucket, cast: smallBucket, airplay: smallBucket }),
});
export type InsightsPayloadDTO = d.Infer<typeof payloadDecoder>;

const statusDecoder = d.object({
  enabled: d.boolean(),
  forced_off: d.boolean(),
  instance_id: d.uuid(),
  notice_acknowledged: d.boolean(),
  first_send_after: d.timestamp(),
  last_attempt_at: d.nullable(d.timestamp()),
  last_success_at: d.nullable(d.timestamp()),
  last_error: d.nullable(d.string({ min: 1, max: 64 })),
  endpoint_host: d.string({ min: 1, max: 256 }),
  public_page_url: d.string({ min: 1, max: 2048, pattern: /^https?:\/\// }),
  payload: payloadDecoder,
});
export type InsightsStatusDTO = d.Infer<typeof statusDecoder>;

export const decodeInsightsStatus = d.defineDecoder(statusDecoder);

/** 登录前也能读：许可与源代码地址，不含版本号。 */
const aboutDecoder = d.object({
  license: d.string({ min: 1, max: 64 }),
  source_url: d.string({ min: 1, max: 2048, pattern: /^https?:\/\// }),
});
export type AboutDTO = d.Infer<typeof aboutDecoder>;
export const decodeAbout = d.defineDecoder(aboutDecoder);
