// 成员写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串。
export const memberFailureMessages = {
  username_taken: "用户名已存在，请换一个。",
  invalid_input: "用户名不能为空，密码至少需要 12 个字节。",
  last_admin: "不能停用最后一个启用的管理员。请先确保还有另一个启用的管理员。",
  not_found: "成员已不存在，列表已刷新。",
} as const;
