import { Activity, ClipboardCheck, Disc3, FileSearch, FileText, History, ListMusic, MicVocal, ScanLine, Search, Wrench, type LucideIcon } from "lucide-react";

// 过程条目的显示：16 个只读工具的中文名称、图标，以及从输入与输出里取“主要参数”和“数量”的规则（工具清单见后端合同第 3 节）。
// 参数与标题来自曲库，是不可信文本，只按文本节点渲染。

type ToolLabel = {
  label: string;
  icon: LucideIcon;
  /** 数量的单位；没有时不显示数量。函数形式按输入决定（搜索的类型）。 */
  unit?: string | ((input: unknown) => string | undefined);
};

const searchUnits: Readonly<Record<string, string>> = { albums: "张", tracks: "首", artists: "位" };

const toolLabels: Readonly<Record<string, ToolLabel>> = {
  search_library: { label: "搜索曲库", icon: Search, unit: (input) => searchUnits[stringField(input, "type") ?? ""] },
  list_albums: { label: "浏览专辑", icon: Disc3, unit: "张" },
  get_album: { label: "读取专辑", icon: FileText, unit: "个版本" },
  get_release: { label: "读取版本", icon: FileText, unit: "首" },
  get_track: { label: "读取曲目", icon: FileText },
  list_artists: { label: "浏览艺人", icon: MicVocal, unit: "位" },
  get_artist: { label: "读取艺人", icon: MicVocal },
  list_artist_albums: { label: "查看艺人的专辑", icon: Disc3, unit: "张" },
  list_artist_tracks: { label: "查看艺人的曲目", icon: ListMusic, unit: "首" },
  get_release_evidence: { label: "读取版本的来源证据", icon: FileSearch },
  list_release_issues: { label: "查看待关注的问题", icon: ClipboardCheck, unit: "个版本" },
  get_release_issues: { label: "查看版本的问题", icon: ClipboardCheck },
  list_scans: { label: "查看扫描记录", icon: ScanLine, unit: "次" },
  get_scan_report: { label: "读取扫描报告", icon: ScanLine },
  get_scan_status: { label: "查看扫描状态", icon: Activity },
  list_operations: { label: "查看修改记录", icon: History, unit: "条" },
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(value: unknown, key: string): string | undefined {
  if (!isRecord(value)) return undefined;
  const field = value[key];
  return typeof field === "string" && field.trim() !== "" ? field : undefined;
}

/** 工具的名称与图标；未登记的工具名原样显示。 */
export function toolLabelOf(name: string): { label: string; icon: LucideIcon } {
  const known = toolLabels[name];
  return known === undefined ? { label: name, icon: Wrench } : { label: known.label, icon: known.icon };
}

/** 主要参数：输入里的搜索词，或结果里对象的标题（专辑、版本、曲目、艺人）；都没有时是 null。 */
export function toolArgumentOf(input: unknown, output: unknown): string | null {
  const query = stringField(input, "q");
  if (query !== undefined) return query;
  if (!isRecord(output)) return null;
  return (
    stringField(output, "title") ??
    stringField(output, "name") ??
    stringField(output["representative"], "title") ??
    stringField(output["artist"], "name") ??
    null
  );
}

/** 结果数量：输出的 pagination.total，没有分页时取 items 的长度；都没有、或工具没有单位时是 null。 */
export function toolCountOf(name: string, input: unknown, output: unknown): string | null {
  const known = toolLabels[name];
  const unit = typeof known?.unit === "function" ? known.unit(input) : known?.unit;
  if (unit === undefined || !isRecord(output)) return null;
  const pagination = output["pagination"];
  const total = isRecord(pagination) ? pagination["total"] : undefined;
  const items = output["items"];
  const count = typeof total === "number" ? total : Array.isArray(items) ? items.length : null;
  return count === null ? null : `${count.toLocaleString("zh-CN")} ${unit}`;
}

const failurePhrases: Readonly<Record<string, string>> = {
  not_found: "没找到",
  permission_denied: "没有权限",
  cancelled: "已停止",
  interrupted: "没有完成",
  // 该步触发了 token 上限，调用没有执行（合同第 7 节）。
  token_limit: "没有执行",
};

/** 失败的工具调用右侧的短语。 */
export function toolFailureOf(errorCode: string | null): string {
  return (errorCode === null ? undefined : failurePhrases[errorCode]) ?? "失败";
}
