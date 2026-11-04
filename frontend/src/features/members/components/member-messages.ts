import { invalidAccountMessage } from "../../auth";

// 成员写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串。
export const memberFailureMessages = {
  username_taken: "用户名已存在，请换一个。",
  invalid_input: invalidAccountMessage,
  last_admin: "不能停用最后一个管理员。",
  not_found: "成员已不存在，列表已刷新。",
} as const;
