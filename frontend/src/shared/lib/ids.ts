// 请求 ID 与幂等键都由 crypto.getRandomValues 生成：crypto.randomUUID 只在安全上下文可用，
// 而开发时常经局域网 HTTP 访问页面。

function randomBytes(length: number): number[] {
  return Array.from(crypto.getRandomValues(new Uint8Array(length)));
}

function toHex(bytes: readonly number[]): string {
  return bytes.map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** 客户端生成的请求关联 ID：web- 加 16 位十六进制，满足后端对 X-Request-ID 的格式要求。 */
export function newRequestId(): string {
  return `web-${toHex(randomBytes(8))}`;
}

/** 目录操作的幂等键（UUID v4）。每个新的操作意图生成一个，结果未知后的重试沿用同一个。 */
export function newIdempotencyKey(): string {
  const bytes = randomBytes(16).map((byte, index) => {
    if (index === 6) return (byte & 0x0f) | 0x40;
    if (index === 8) return (byte & 0x3f) | 0x80;
    return byte;
  });
  const hex = toHex(bytes);
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
