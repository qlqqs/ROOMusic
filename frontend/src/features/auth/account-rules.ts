// 新账号（初始化管理员与成员管理新建成员）的输入规则。后端是权威：用户名只检查去空白后非空，
// 密码需要 12 到 72 个字符，且只能是可打印 ASCII（U+0020–U+007E），所以字符数等于后端按字节计的长度；
// 这里只是提前给出可读的字段提示，不替代后端校验。

export const MIN_PASSWORD_LENGTH = 12;
/** bcrypt 只处理前 72 个字节，后端拒绝更长的密码（400 invalid_input）。 */
export const MAX_PASSWORD_LENGTH = 72;

/** 可打印 ASCII：英文字母、数字、符号和空格。 */
const PASSWORD_PATTERN = /^[\x20-\x7E]*$/;

/** 后端按字节数检查长度（Go 的 len），所以英文每字符 1 字节、常见中文字符 3 字节。 */
export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export type NewAccountInput = { username: string; password: string; confirmation: string };
export type NewAccountErrors = { username?: string; password?: string; confirmation?: string };

export const passwordHint = `至少 ${MIN_PASSWORD_LENGTH} 位，可用英文字母、数字和符号。`;

/** 后端以 invalid_input 拒绝不合规的用户名或密码时显示的说明（界面按错误码选文案）。 */
export const invalidAccountMessage = `用户名不能为空，密码至少 ${MIN_PASSWORD_LENGTH} 位，只能用英文字母、数字和符号。`;

export function validateNewAccount(input: NewAccountInput): NewAccountErrors {
  const errors: NewAccountErrors = {};
  if (input.username.trim() === "") errors.username = "请输入用户名。";
  if (!PASSWORD_PATTERN.test(input.password)) errors.password = "密码只能用英文字母、数字和符号。";
  else if (input.password.length < MIN_PASSWORD_LENGTH) errors.password = `密码至少要 ${MIN_PASSWORD_LENGTH} 位。`;
  else if (input.password.length > MAX_PASSWORD_LENGTH) errors.password = "密码太长了，请短一些。";
  else if (input.confirmation !== input.password) errors.confirmation = "两次输入的密码不一致。";
  return errors;
}
