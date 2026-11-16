import { decode as d } from "../../../shared/api";

export const tokenScopes = ["read", "read_write"] as const;
export type TokenScope = (typeof tokenScopes)[number];

/** 每个用户最多这么多个未吊销的令牌（后端的上限）。 */
export const MAX_TOKENS = 20;

const tokenShape = {
  id: d.uuid(),
  name: d.string({ min: 1 }),
  scope: d.oneOf(tokenScopes),
  /** 明文的末四位，用来在列表里认出是哪一个。 */
  tail: d.string({ min: 1 }),
  created_at: d.timestamp(),
  expires_at: d.nullable(d.timestamp()),
  last_used_at: d.nullable(d.timestamp()),
};

const tokenDecoder = d.object(tokenShape);

// available 为 false：管理员关闭了成员使用管家，成员的令牌暂时不能用，也不能新建。
const tokenListDecoder = d.object({ available: d.boolean(), tokens: d.array(tokenDecoder, { max: MAX_TOKENS }) });

export const decodeTokenList = d.defineDecoder(tokenListDecoder);
export type TokenDTO = d.Infer<typeof tokenDecoder>;
export type TokenListDTO = d.Infer<typeof tokenListDecoder>;

// 签发的响应：明文只在这里出现一次，之后任何接口都不再返回。
const createdTokenDecoder = d.object({ ...tokenShape, token: d.string({ min: 1 }) });

export const decodeCreatedToken = d.defineDecoder(createdTokenDecoder);
export type CreatedTokenDTO = d.Infer<typeof createdTokenDecoder>;
