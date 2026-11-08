// 目录与扫描写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串。

export const rootFailureMessages = {
  invalid_library_path: "这个路径不在允许的音乐目录里，或者不是绝对路径。",
  revision_conflict: "目录刚有变化，列表已刷新，请看一下再操作。",
  idempotency_conflict: "操作冲突了，请再试一次。",
  not_found: "目录已不存在，列表已刷新。",
} as const;

/** 不扫描的子文件夹的增删：路径被拒绝时说文件夹打不开（目录浏览器只给出目录之内的文件夹）。 */
export const exclusionFailureMessages = {
  ...rootFailureMessages,
  invalid_library_path: "打不开这个文件夹。",
  exclusion_limit: "不扫描的文件夹太多了。",
} as const;

/** 改路径：路径来自目录浏览器，被拒绝时说文件夹打不开。 */
export const relocateFailureMessages = {
  ...rootFailureMessages,
  invalid_library_path: "打不开这个文件夹。",
  revision_conflict: "目录刚被改过，请再试一次。",
  scan_running: "正在扫描，稍后再试。",
  path_already_registered: "这个文件夹已经添加过了。",
} as const;

export const scanFailureMessages = {
  scan_coordination_unavailable: "暂时无法协调扫描任务，请稍后重试。",
  not_found: "扫描不存在。",
} as const;
