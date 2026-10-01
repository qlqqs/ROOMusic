// 新账号（初始化管理员与成员管理新建成员）的输入规则。后端是权威：用户名只检查去空白后非空，
// 密码长度按 UTF-8 字节计算且至少 12 字节；这里只是提前给出可读的字段提示，不替代后端校验。

export const MIN_PASSWORD_BYTES = 12;

/** 后端按字节数检查密码长度（Go 的 len），所以英文每字符 1 字节、常见中文字符 3 字节。 */
export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export type NewAccountInput = { username: string; password: string; confirmation: string };
export type NewAccountErrors = { username?: string; password?: string; confirmation?: string };

export const passwordHint = `至少 ${MIN_PASSWORD_BYTES} 个字节（英文字符占 1 个字节，中文字符占 3 个字节）。`;

export function validateNewAccount(input: NewAccountInput): NewAccountErrors {
  const errors: NewAccountErrors = {};
  if (input.username.trim() === "") errors.username = "请输入用户名。";
  if (utf8ByteLength(input.password) < MIN_PASSWORD_BYTES) errors.password = `密码至少需要 ${MIN_PASSWORD_BYTES} 个字节。`;
  else if (input.confirmation !== input.password) errors.confirmation = "两次输入的密码不一致。";
  return errors;
}
