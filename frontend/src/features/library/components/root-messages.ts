// 目录与扫描写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串。

export const rootFailureMessages = {
  invalid_library_path: "路径不在服务器允许的音乐根目录内，或不是有效的绝对路径。",
  revision_conflict: "目录已发生变化，列表已刷新。请确认目录当前的状态后再重新操作。",
  idempotency_conflict: "这次操作的标识与之前的另一个请求冲突。请重新发起操作。",
  not_found: "目录已不存在，列表已刷新。",
} as const;

export const scanFailureMessages = {
  scan_coordination_unavailable: "暂时无法协调扫描任务，请稍后重试。",
  not_found: "扫描不存在。",
} as const;
