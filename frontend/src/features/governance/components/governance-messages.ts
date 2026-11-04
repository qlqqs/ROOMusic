// 管理员对发行的写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串；
// 没有列出的错误码（例如 invalid_correction，后端给出的是具体的、可以直接展示的说明）显示后端的安全 message 与请求 ID。

export const governanceFailureMessages = {
  revision_conflict: "这张专辑刚被修改过，页面已刷新。请核对当前内容后再操作。",
  idempotency_conflict: "操作冲突了，请再试一次。",
  not_found: "这个版本已经找不到了，可能刚重新扫描过。",
  group_not_found: "目标专辑已不存在，或者没有可见的版本。请重新搜索。",
  not_in_group: "这个版本已经不在这张专辑里了，页面已刷新。",
  same_group: "这个版本已经在这张专辑里了。",
  already_alone: "这个版本已经是独立专辑，不需要再移出。",
  no_changes: "没有需要修改的内容。",
  not_revertible: "这条记录不能撤销。",
  already_reverted: "这条操作已经撤销过了，记录已刷新。",
  revert_conflict: "这张专辑之后又被修改过，不能撤销这一条。只有最近一次修改可以撤销。",
  issue_changed: "扫描刚刚改变了这个问题，已重新读取，请看过之后再核对。",
  forbidden: "你没有权限做这个操作。",
} as const;
