import { defineDecoder, object, string } from "./decode";

// 统一错误信封 {error:{code,message}, request_id}。code 只含小写字母、数字与下划线；
// request_id 允许为空，因为不经请求中间件的降级 handler 可能不回写它。
export const decodeErrorEnvelope = defineDecoder(
  object({
    error: object({
      code: string({ min: 1, max: 64, pattern: /^[a-z0-9_]+$/ }),
      message: string({ max: 1000 }),
    }),
    request_id: string({ max: 128 }),
  }),
);

export type ErrorEnvelope = ReturnType<typeof decodeErrorEnvelope>;
