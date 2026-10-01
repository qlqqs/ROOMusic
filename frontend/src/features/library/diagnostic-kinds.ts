// 诊断类别的中文名。清单按 2026-10-01 后端写入 scan_diagnostics 的位置逐一核对：
// internal/library/scanner.go（扫描与 CUE、抓轨日志）、internal/catalog/identity_plan.go（曲目身份规划）、
// internal/library/scans.go 的 safeDiagnosticMessage（还保留了 walk_error 与 staging_read_failure 的旧类别）。
// 后端把不合法或超长的类别归一为 "unknown"。新增类别不会让界面出错：未知类别原样显示。
const diagnosticKindNames: ReadonlyMap<string, string> = new Map(
  Object.entries({
    root_unavailable: "目录不可用",
    walk_error: "目录部分内容无法读取",
    permission_error: "没有读取权限",
    broken_or_escaped_link: "链接目标不可读取",
    non_regular_file: "不是普通文件",
    unsupported_cue: "CUE 无法处理",
    cue_reference: "CUE 引用问题",
    parse_failure: "音频解析失败",
    unsupported_format: "格式暂不支持",
    rip_log_unreadable: "抓轨日志无法读取",
    identity_ambiguous: "曲目身份不明确",
    content_replaced: "同一路径的内容已变化",
    identity_deferred: "身份判断留待完整扫描",
    fingerprint_failure: "内容指纹计算失败",
    staging_write_failure: "扫描暂存写入失败",
    staging_read_failure: "扫描暂存读取失败",
    catalog_write_failure: "资料库保存失败",
    artwork_failure: "封面处理失败",
    database_error: "数据库操作失败",
    unknown: "未知类别",
  }),
);

/** 已知类别显示中文名，未知类别原样显示（类别只含小写字母、数字与下划线，可以安全显示）。 */
export function diagnosticKindLabel(kind: string): string {
  return diagnosticKindNames.get(kind) ?? kind;
}

export function isKnownDiagnosticKind(kind: string): boolean {
  return diagnosticKindNames.has(kind);
}
