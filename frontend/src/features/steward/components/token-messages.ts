import type { TokenScope } from "../api/decoders";

export const scopeLabels: Record<TokenScope, string> = { read: "只读", read_write: "读写" };

// 令牌写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串。
export const tokenFailureMessages = {
  steward_disabled: "管理员已关闭成员使用管家，现在不能新建令牌。",
  token_limit_reached: "令牌数量已到上限，请先吊销不用的令牌。",
  invalid_input: "名称需要 1 到 64 个字。",
  not_found: "令牌已不存在，列表已刷新。",
} as const;
