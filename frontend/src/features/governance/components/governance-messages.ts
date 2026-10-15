// 管理员对发行的写操作按错误码覆盖的文案：界面按错误码选文案，不比较后端 message 的字符串；
// 没有列出的错误码（例如 invalid_correction，后端给出的是具体的、可以直接展示的说明）显示后端的安全 message 与请求 ID。

export const governanceFailureMessages = {
  revision_conflict: "这张专辑刚被修改过，页面已刷新。请核对当前内容后再操作。",
  idempotency_conflict: "这次操作的标识与之前的另一个请求冲突。请重新发起操作。",
  not_found: "这个版本已不可见（可能刚被重新扫描），无法继续操作。",
  group_not_found: "目标专辑已不存在，或者没有可见的版本。请重新搜索。",
  not_in_group: "这个版本已不在这张专辑里，或现在不可见，页面已刷新。请核对后再操作。",
  same_group: "这个版本已经在这张专辑里了。",
  already_alone: "这个版本已经是独立专辑，不需要再移出。",
  no_changes: "没有需要修改的内容。",
  not_revertible: "这条记录不能撤销。",
  already_reverted: "这条操作已经撤销过了，记录已刷新。",
  revert_conflict: "这张专辑之后又被修改过，不能撤销这一条。只有最近一次修改可以撤销。",
  forbidden: "你的账号没有执行这个操作的权限。",
} as const;
