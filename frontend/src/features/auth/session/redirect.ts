const MAX_REDIRECT_LENGTH = 2048;

// 登录页与初始化页本身不能是登录后的去向，否则会在两者之间打转。
const authPaths = ["/login", "/setup"];

function isAuthPath(value: string): boolean {
  return authPaths.some((path) => value === path || ["/", "?", "#"].some((separator) => value.startsWith(path + separator)));
}

/**
 * 校验 `/login?redirect=` 的值：只接受站内路径，防止开放重定向。
 * 必须以单个 `/` 开头、不超过 2048 个字符、不含反斜杠与控制字符，也不能指向登录页或初始化页；
 * 不合法时返回 undefined，调用方回到概览。
 */
export function sanitizeRedirect(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length === 0 || value.length > MAX_REDIRECT_LENGTH) return undefined;
  if (!value.startsWith("/") || value.startsWith("//")) return undefined;
  if (value.includes("\\") || Array.from(value).some((character) => character.charCodeAt(0) < 0x20 || character.charCodeAt(0) === 0x7f)) {
    return undefined;
  }
  if (isAuthPath(value)) return undefined;
  return value;
}
