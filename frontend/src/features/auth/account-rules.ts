// 新账号（初始化管理员与成员管理新建成员）与新密码（自助改密码、管理员重置）的输入规则。后端是权威：用户名只检查去空白后非空，
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

/** 后端以 invalid_input 拒绝新密码（自助改密码与管理员重置）时显示的说明。 */
export const newPasswordRuleMessage = `新密码至少 ${MIN_PASSWORD_LENGTH} 位，只能用英文字母、数字和符号。`;

export type NewPasswordErrors = { password?: string; confirmation?: string };

/** 新密码与“再输入一次”：新建账号、自助改密码与管理员重置共用。 */
export function validateNewPassword(password: string, confirmation: string): NewPasswordErrors {
  if (!PASSWORD_PATTERN.test(password)) return { password: "密码只能用英文字母、数字和符号。" };
  if (password.length < MIN_PASSWORD_LENGTH) return { password: `密码至少要 ${MIN_PASSWORD_LENGTH} 位。` };
  if (password.length > MAX_PASSWORD_LENGTH) return { password: "密码太长了，请短一些。" };
  if (confirmation !== password) return { confirmation: "两次输入的密码不一致。" };
  return {};
}

export function validateNewAccount(input: NewAccountInput): NewAccountErrors {
  const errors: NewAccountErrors = validateNewPassword(input.password, input.confirmation);
  if (input.username.trim() === "") errors.username = "请输入用户名。";
  return errors;
}

/** current 为 null 表示不需要当前密码（管理员）。 */
export type PasswordChangeInput = { current: string | null; password: string; confirmation: string };
export type PasswordChangeErrors = NewPasswordErrors & { current?: string };

/** 自助改密码：需要当前密码时只检查非空（对不对由后端判断），新密码沿用新账号的规则。 */
export function validatePasswordChange(input: PasswordChangeInput): PasswordChangeErrors {
  const errors: PasswordChangeErrors = validateNewPassword(input.password, input.confirmation);
  if (input.current === "") errors.current = "请输入当前密码。";
  return errors;
}
