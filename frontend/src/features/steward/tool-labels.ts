import {
  Activity,
  ArrowUpDown,
  Cast,
  ClipboardCheck,
  Disc3,
  FileSearch,
  FileText,
  Heart,
  History,
  ListMinus,
  ListMusic,
  ListOrdered,
  ListPlus,
  MicVocal,
  Pencil,
  Play,
  Radio,
  Repeat,
  ScanLine,
  Search,
  Trash2,
  Volume2,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { StewardDataScope } from "./context/host";

// 过程条目的显示：工具的中文名称、图标，以及从输入与输出里取“主要参数”和“结果”的规则（工具清单见后端合同第 3 节）。
// 只读工具 16 个（C2）；C5 加了播放、队列、播放历史、收藏与歌单的工具。参数与标题来自曲库与个人数据，是不可信文本，只按文本节点渲染。

type ToolLabel = {
  label: string;
  icon: LucideIcon;
  /** 数量的单位；没有时不显示数量。函数形式按输入决定（搜索的类型、收藏的种类）。 */
  unit?: string | ((input: unknown) => string | undefined);
  /** 主要参数：按输入取的动作、模式或名字；取不到时按通用规则（搜索词、结果里对象的标题）。 */
  argument?: (input: unknown) => string | undefined;
  /** 成功后右侧的结果说明（改动类工具）；取不到时显示数量。 */
  result?: (input: unknown, output: unknown) => string | undefined;
  /** 这个工具会改动的个人数据：结束后让对应查询失效。有它就是改动类工具。 */
  changes?: StewardDataScope;
  /** 改动类工具但不用刷新查询（播放控制，分区事件会推到浏览器）。 */
  writes?: true;
};

const searchUnits: Readonly<Record<string, string>> = { albums: "张", tracks: "首", artists: "位" };
const favoriteUnits: Readonly<Record<string, string>> = { track: "首", tracks: "首", album: "张", albums: "张", artist: "位", artists: "位" };
const favoriteKinds: Readonly<Record<string, string>> = { track: "曲目", tracks: "曲目", album: "专辑", albums: "专辑", artist: "艺人", artists: "艺人" };
const playbackActions: Readonly<Record<string, string>> = { play: "播放", pause: "暂停", stop: "停止", next: "下一首", previous: "上一首", seek: "跳转" };
const placements: Readonly<Record<string, string>> = { replace: "替换队列", next: "下一首播放", end: "加到队尾" };
const queueActions: Readonly<Record<string, string>> = { remove: "移除", move: "移动", clear: "清空", play_entry: "播放这一首" };
const playModes: Readonly<Record<string, string>> = { sequential: "顺序", repeat_all: "列表循环", repeat_one: "单曲循环", shuffle: "随机" };

/** 输入里某个枚举字段对应的中文。 */
const pick = (key: string, names: Readonly<Record<string, string>>) => (input: unknown) => names[stringField(input, key) ?? ""];

/** 输入里 ID 列表的长度，写成“N 首”。 */
function trackCountOf(input: unknown): string | undefined {
  const ids = isRecord(input) ? input["track_ids"] : undefined;
  return Array.isArray(ids) && ids.length > 0 ? `${ids.length.toLocaleString("zh-CN")} 首` : undefined;
}

function volumeOf(input: unknown): string | undefined {
  if (!isRecord(input)) return undefined;
  if (input["muted"] === true) return "静音";
  const volume = input["volume"];
  if (typeof volume === "number" && volume >= 0 && volume <= 1) return `音量 ${Math.round(volume * 100)}%`;
  return input["muted"] === false ? "取消静音" : undefined;
}

/** 收藏还是取消收藏：看输入的 favorited，没有时看结果的。 */
function favoritedOf(input: unknown, output: unknown): string | undefined {
  const favorited = isRecord(input) && typeof input["favorited"] === "boolean" ? input["favorited"] : isRecord(output) ? output["favorited"] : undefined;
  return favorited === true ? "已收藏" : favorited === false ? "已取消收藏" : undefined;
}

const done = (phrase: string) => () => phrase;

const toolLabels: Readonly<Record<string, ToolLabel>> = {
  // ---- 曲库（只读，C2）----
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

  // ---- 播放与队列（C5）：分区事件会推到浏览器，不用另外刷新 ----
  get_playback: { label: "查看播放状态", icon: Radio },
  get_queue: { label: "查看播放队列", icon: ListOrdered, unit: "首" },
  control_playback: { label: "控制播放", icon: Play, argument: pick("action", playbackActions), writes: true },
  play_tracks: { label: "播放曲目", icon: ListPlus, argument: pick("placement", placements), result: trackCountOf, writes: true },
  edit_queue: { label: "编辑播放队列", icon: ListOrdered, argument: pick("action", queueActions), writes: true },
  set_play_mode: { label: "设置播放模式", icon: Repeat, argument: pick("mode", playModes), writes: true },
  transfer_playback: { label: "转移播放", icon: Cast, writes: true },
  set_device_volume: { label: "调整音量", icon: Volume2, argument: volumeOf, writes: true },

  // ---- 播放历史（C5）----
  list_play_history: { label: "查看播放历史", icon: History, unit: "首" },
  list_recent_releases: { label: "查看最近播放的专辑", icon: Disc3, unit: "张" },
  delete_play_history_item: { label: "删除一条播放历史", icon: Trash2, result: done("已删除"), changes: "history" },

  // ---- 收藏与歌单（C5）----
  list_favorites: {
    label: "查看收藏",
    icon: Heart,
    unit: (input) => favoriteUnits[stringField(input, "kind") ?? ""],
    argument: pick("kind", favoriteKinds),
  },
  list_playlists: { label: "查看歌单", icon: ListMusic, unit: "个" },
  get_playlist: { label: "读取歌单", icon: ListMusic, unit: "首" },
  set_favorite: { label: "收藏", icon: Heart, argument: pick("kind", favoriteKinds), result: favoritedOf, changes: "favorites" },
  create_playlist: { label: "新建歌单", icon: ListPlus, argument: (input) => stringField(input, "name"), result: done("已新建"), changes: "playlists" },
  rename_playlist: { label: "改歌单名", icon: Pencil, argument: (input) => stringField(input, "name"), result: done("已改名"), changes: "playlists" },
  delete_playlist: { label: "删除歌单", icon: Trash2, result: done("已删除"), changes: "playlists" },
  add_playlist_tracks: { label: "加到歌单", icon: ListPlus, result: trackCountOf, changes: "playlists" },
  remove_playlist_track: { label: "从歌单移除曲目", icon: ListMinus, result: done("已移除"), changes: "playlists" },
  move_playlist_track: { label: "调整歌单曲序", icon: ArrowUpDown, result: done("已移动"), changes: "playlists" },
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

/**
 * 主要参数：工具自己的规则（动作、模式、歌单名）优先；否则是输入里的搜索词，或结果里对象的标题
 * （专辑、版本、曲目、艺人、歌单）；都没有时是 null。
 */
export function toolArgumentOf(name: string, input: unknown, output: unknown): string | null {
  const own = toolLabels[name]?.argument?.(input);
  if (own !== undefined) return own;
  const query = stringField(input, "q");
  if (query !== undefined) return query;
  if (!isRecord(output)) return null;
  return (
    stringField(output, "title") ??
    stringField(output, "name") ??
    stringField(output["representative"], "title") ??
    stringField(output["artist"], "name") ??
    stringField(output["playlist"], "name") ??
    stringField(output["track"], "title") ??
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

/** 成功的工具调用右侧的说明：改动类工具的结果（“已收藏”“3 首”），其余是数量；都没有时是 null。 */
export function toolResultOf(name: string, input: unknown, output: unknown): string | null {
  return toolLabels[name]?.result?.(input, output) ?? toolCountOf(name, input, output);
}

/** 改动类工具（播放控制、收藏、歌单与历史的写入）；只读工具与未登记的工具不算。 */
export function toolWritesOf(name: string): boolean {
  const known = toolLabels[name];
  return known?.writes === true || known?.changes !== undefined;
}

/** 这个工具会改动哪类个人数据；只读与播放类工具是 null。 */
export function toolDataScopeOf(name: string): StewardDataScope | null {
  return toolLabels[name]?.changes ?? null;
}

const failurePhrases: Readonly<Record<string, string>> = {
  not_found: "没找到",
  permission_denied: "没有权限",
  cancelled: "已停止",
  interrupted: "没有完成",
  // 该步触发了 token 上限，调用没有执行（合同第 7 节）。
  token_limit: "没有执行",
  zone_offline: "分区不在线",
  renderer_unavailable: "没有可用的输出",
  // 歌单在读取之后被改过（版本号对不上），需要重新读取。
  revision_conflict: "内容已变化",
  queue_full: "队列已满",
  volume_fixed: "这个设备的音量不能调",
  playlist_limit_reached: "歌单数量已到上限",
  playlist_track_limit_reached: "歌单曲目已到上限",
  no_playable_tracks: "没有可播放的曲目",
};

/** 失败的工具调用右侧的短语。 */
export function toolFailureOf(errorCode: string | null): string {
  return (errorCode === null ? undefined : failurePhrases[errorCode]) ?? "失败";
}
