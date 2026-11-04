// 新账号（初始化管理员与成员管理新建成员）的输入规则。后端是权威：用户名只检查去空白后非空，
// 密码长度按 UTF-8 字节计算，需要 12 到 72 字节；这里只是提前给出可读的字段提示，不替代后端校验。

export const MIN_PASSWORD_BYTES = 12;
/** bcrypt 只处理前 72 个字节，后端拒绝更长的密码（400 invalid_input）。 */
export const MAX_PASSWORD_BYTES = 72;

/** 后端按字节数检查密码长度（Go 的 len），所以英文每字符 1 字节、常见中文字符 3 字节。 */
export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export type NewAccountInput = { username: string; password: string; confirmation: string };
export type NewAccountErrors = { username?: string; password?: string; confirmation?: string };

export const passwordHint = `至少 ${MIN_PASSWORD_BYTES} 个字符。`;

/** 后端以 invalid_input 拒绝不合规的用户名或密码时显示的说明（界面按错误码选文案）。 */
export const invalidAccountMessage = `用户名不能为空，密码至少 ${MIN_PASSWORD_BYTES} 个字符，也不能太长。`;

export function validateNewAccount(input: NewAccountInput): NewAccountErrors {
  const errors: NewAccountErrors = {};
  if (input.username.trim() === "") errors.username = "请输入用户名。";
  const passwordBytes = utf8ByteLength(input.password);
  if (passwordBytes < MIN_PASSWORD_BYTES) errors.password = `密码至少要 ${MIN_PASSWORD_BYTES} 个字符。`;
  else if (passwordBytes > MAX_PASSWORD_BYTES) errors.password = "密码太长了，请短一些。";
  else if (input.confirmation !== input.password) errors.confirmation = "两次输入的密码不一致。";
  return errors;
}
