import {
  Activity,
  ArrowUpDown,
  BookOpen,
  Bookmark,
  BookmarkMinus,
  BookmarkPlus,
  Cast,
  ClipboardCheck,
  Disc3,
  EyeOff,
  FileInput,
  FileSearch,
  FileText,
  Globe,
  Heart,
  History,
  Layers,
  Library,
  ListMinus,
  ListMusic,
  ListOrdered,
  ListPlus,
  Merge,
  MicVocal,
  Newspaper,
  Pencil,
  PenLine,
  Pin,
  Play,
  Radio,
  Repeat,
  ScanLine,
  Search,
  Split,
  Trash2,
  Undo2,
  Volume2,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { StewardDataScope } from "./context/host";
import { NATIVE_SEARCH } from "./run/reduce";

// 过程条目的显示：工具的中文名称、图标，以及从输入与输出里取“主要参数”和“结果”的规则（工具清单见后端合同第 3 节）。
// 只读工具 16 个（C2）；C5 加了播放、队列、播放历史、收藏与歌单的工具；C6 加了曲库与艺人的治理工具（管理员，修改前要确认）。
// C8 加了联网的四个工具（网页搜索、读取网页、MusicBrainz 的搜索与读取）与模型自带搜索的条目（NATIVE_SEARCH）：
// 只显示查询词与访问的域名，不显示完整地址（查询串可能带别的内容）。
// C9 加了诊断、应用配置与个人播放设置的工具：配置类的修改不能撤销，确认卡片上会标明。
// C10 加了记忆的三个工具（只作用于本人）：记下或改写、删除之后让设置页的记忆列表重读。
// 参数与标题来自曲库、个人数据与外部网页，是不可信文本，只按文本节点渲染。

/** 工具改动的数据：app 负责的几类（StewardDataScope），加上管家自己的记忆（C10，由 steward 自己刷新）。 */
export type ToolDataScope = StewardDataScope | "memories";

type ToolLabel = {
  label: string;
  icon: LucideIcon;
  /** 数量的单位；没有时不显示数量。函数形式按输入决定（搜索的类型、收藏的种类）。 */
  unit?: string | ((input: unknown) => string | undefined);
  /** 主要参数：按输入（个别工具也看输出）取的动作、模式、名字或域名；取不到时按通用规则（搜索词、结果里对象的标题）。 */
  argument?: (input: unknown, output: unknown) => string | undefined;
  /** 成功后右侧的结果说明（改动类工具）；取不到时显示数量。 */
  result?: (input: unknown, output: unknown) => string | undefined;
  /** 这个工具会改动的数据：结束后让对应查询失效。有它就是改动类工具。 */
  changes?: ToolDataScope;
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

/** 修正专辑字段的字段名（与发行信息的编辑对话框一致）。 */
export const releaseFieldLabels: Readonly<Record<string, string>> = {
  title: "标题",
  album_artist: "专辑艺人",
  year: "年份",
  genre: "流派",
  label: "厂牌",
  catalog_number: "编号",
  edition: "版本说明",
  barcode: "条码",
};

/** correct_release 改了哪些字段：“标题、年份”。 */
function correctedFieldsOf(input: unknown): string | undefined {
  const changes = isRecord(input) ? input["changes"] : undefined;
  if (!isRecord(changes)) return undefined;
  const names = Object.keys(changes).map((key) => releaseFieldLabels[key] ?? key);
  return names.length === 0 ? undefined : names.join("、");
}

const groupActions: Readonly<Record<string, string>> = { join: "归入其他专辑", detach: "移出为独立专辑" };
const reviewActions: Readonly<Record<string, string>> = { review: "核对", unreview: "取消核对" };
const reviewResults: Readonly<Record<string, string>> = { review: "已核对", unreview: "已取消核对" };

/** set_default_release：release_id 为 null 是改回自动选择。 */
function defaultReleaseOf(input: unknown): string | undefined {
  return isRecord(input) && input["release_id"] === null ? "改回自动选择" : undefined;
}

/** rename_artist：mode=auto 是改回最常见的写法，否则是新名字。 */
function renameOf(input: unknown): string | undefined {
  return stringField(input, "mode") === "auto" ? "改回最常见的写法" : stringField(input, "name");
}

/** ignore_artist_candidate：ignored 为 false 是恢复提示。 */
function ignoredOf(input: unknown, phrases: readonly [string, string]): string | undefined {
  if (!isRecord(input) || typeof input["ignored"] !== "boolean") return undefined;
  return input["ignored"] ? phrases[0] : phrases[1];
}

/** 地址的域名（去掉开头的 www.）；不是 http、https 的地址是 null。只显示域名，不显示路径与查询串。 */
export function domainOf(url: string): string | null {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
    return parsed.hostname.replace(/^www\./, "");
  } catch {
    return null;
  }
}

/** fetch_page 访问的域名：跳转到了别的域名时写“甲 → 乙”。 */
function fetchedDomainOf(input: unknown, output: unknown): string | undefined {
  const requested = domainOf(stringField(input, "url") ?? "");
  const final = domainOf(stringField(output, "final_url") ?? "");
  if (requested === null) return final ?? undefined;
  return final !== null && final !== requested ? `${requested} → ${final}` : requested;
}

function fetchedOf(_input: unknown, output: unknown): string | undefined {
  return isRecord(output) && output["truncated"] === true ? "已读取，过长的部分截掉了" : "已读取";
}

/** 结果列表的条数：输出的 results，没有时 items。 */
function resultCountOf(output: unknown, unit: string): string | undefined {
  if (!isRecord(output)) return undefined;
  const list = Array.isArray(output["results"]) ? output["results"] : Array.isArray(output["items"]) ? output["items"] : null;
  return list === null ? undefined : `${list.length.toLocaleString("zh-CN")} ${unit}`;
}

const musicEntities: Readonly<Record<string, string>> = { artist: "艺人", release: "版本", "release-group": "专辑", recording: "录音" };

/** search_music_metadata：“专辑：查询词”。 */
function musicQueryOf(input: unknown): string | undefined {
  const query = stringField(input, "query");
  const entity = musicEntities[stringField(input, "entity") ?? ""];
  if (query === undefined) return entity;
  return entity === undefined ? query : `${entity}：${query}`;
}

/** get_music_metadata：结果的标题或名字，没有时是条目的种类。 */
function musicEntryOf(input: unknown, output: unknown): string | undefined {
  return stringField(output, "title") ?? stringField(output, "name") ?? musicEntities[stringField(input, "entity") ?? ""];
}

const searchResultsOf = (_input: unknown, output: unknown) => resultCountOf(output, "条结果");

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

  // ---- 曲库与艺人的治理（C6，管理员）：assistant 模式下先合成方案给用户确认 ----
  correct_release: { label: "修正专辑信息", icon: PenLine, argument: correctedFieldsOf, result: done("已修改"), changes: "catalog" },
  change_release_group: { label: "调整归组", icon: Layers, argument: pick("action", groupActions), result: done("已调整"), changes: "catalog" },
  set_default_release: { label: "设定默认版本", icon: Pin, argument: defaultReleaseOf, result: done("已设定"), changes: "catalog" },
  review_release_issue: {
    label: "核对问题",
    icon: ClipboardCheck,
    argument: pick("action", reviewActions),
    result: (input) => reviewResults[stringField(input, "action") ?? ""] ?? "已完成",
    changes: "catalog",
  },
  preview_release_claim: { label: "预览认领", icon: FileSearch },
  claim_release: { label: "认领修改", icon: FileInput, result: done("已认领"), changes: "catalog" },
  merge_artists: { label: "合并艺人", icon: Merge, result: done("已合并"), changes: "artists" },
  preview_artist_split: { label: "预览拆开艺人", icon: FileSearch },
  split_artist: { label: "拆开艺人", icon: Split, result: done("已拆开"), changes: "artists" },
  rename_artist: { label: "修改艺人主名", icon: Pencil, argument: renameOf, result: done("已改名"), changes: "artists" },
  ignore_artist_candidate: {
    label: "忽略重复艺人提示",
    icon: EyeOff,
    argument: (input) => ignoredOf(input, ["忽略", "恢复提示"]),
    result: (input) => ignoredOf(input, ["已忽略", "已恢复"]),
    changes: "artists",
  },
  revert_operation: { label: "撤销一条修改", icon: Undo2, result: done("已撤销"), changes: "catalog" },

  // ---- 联网（C8，只读）：外部内容带来源标记进入模型 ----
  web_search: { label: "联网搜索", icon: Globe, result: searchResultsOf },
  [NATIVE_SEARCH]: { label: "联网搜索（模型自带）", icon: Globe, result: searchResultsOf },
  fetch_page: { label: "读取网页", icon: Newspaper, argument: fetchedDomainOf, result: fetchedOf },
  search_music_metadata: { label: "查 MusicBrainz", icon: Library, argument: musicQueryOf, result: (_input, output) => resultCountOf(output, "条") },
  get_music_metadata: { label: "读取 MusicBrainz 条目", icon: BookOpen, argument: musicEntryOf },
  get_system_status: { label: "查看系统状态", icon: Activity },
  get_disk_usage: { label: "查看磁盘占用", icon: Activity },
  list_recent_logs: { label: "查看最近日志", icon: FileText, unit: "条" },
  get_backup_overview: { label: "查看备份", icon: FileSearch },
  start_backup: { label: "发起备份", icon: Wrench, result: done("已发起"), writes: true },
  set_backup_retention: { label: "修改备份保留策略", icon: Wrench, result: done("已保存"), writes: true },
  list_library_roots: { label: "查看音乐目录", icon: Library },
  get_scan_schedule: { label: "查看扫描计划", icon: ScanLine },
  add_library_root: { label: "添加音乐目录", icon: Library, result: done("已添加"), writes: true },
  set_library_root_enabled: { label: "停用或恢复音乐目录", icon: Library, result: done("已修改"), writes: true },
  add_root_exclusion: { label: "添加排除规则", icon: EyeOff, result: done("已添加"), writes: true },
  remove_root_exclusion: { label: "移除排除规则", icon: EyeOff, result: done("已移除"), writes: true },
  start_scan: { label: "启动扫描", icon: ScanLine, result: done("已启动"), writes: true },
  cancel_scan: { label: "取消扫描", icon: ScanLine, result: done("已取消"), writes: true },
  set_scan_schedule: { label: "修改扫描计划", icon: ScanLine, result: done("已保存"), writes: true },
  list_members: { label: "查看成员", icon: FileSearch, unit: "位" },
  set_member_disabled: { label: "停用或启用成员", icon: Wrench, result: done("已修改"), writes: true },
  revoke_member_sessions: { label: "吊销成员的登录", icon: Wrench, result: done("已吊销"), writes: true },
  get_track_audio: { label: "查看音频信息", icon: Activity },
  get_analysis_progress: { label: "查看音频分析进度", icon: Activity },
  get_zone_settings: { label: "查看播放与音效设置", icon: Volume2 },
  set_zone_settings: { label: "修改播放与音效设置", icon: Volume2, result: done("已修改"), writes: true },
  list_peq_presets: { label: "查看 PEQ 预设", icon: Volume2, unit: "个" },
  apply_peq_preset: { label: "应用 PEQ 预设", icon: Volume2, result: done("已应用"), writes: true },

  // ---- 记忆（C10，只作用于本人）：主要参数是记下的内容 ----
  remember: {
    label: "记住",
    icon: BookmarkPlus,
    argument: (input) => stringField(input, "content"),
    result: (input) => (stringField(input, "id") === undefined ? "已记下" : "已改写"),
    changes: "memories",
  },
  forget: { label: "忘掉一条记忆", icon: BookmarkMinus, result: done("已删除"), changes: "memories" },
  list_memories: { label: "查看记忆", icon: Bookmark, unit: "条" },
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
  const own = toolLabels[name]?.argument?.(input, output);
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

/** 搜索结果里的来源（web_search 与模型自带搜索）：标题与地址；标题可能为空，没有可用地址的项不列。 */
export function toolSourcesOf(name: string, output: unknown): { title: string; url: string }[] {
  if ((name !== "web_search" && name !== NATIVE_SEARCH) || !isRecord(output) || !Array.isArray(output["results"])) return [];
  const sources: { title: string; url: string }[] = [];
  for (const result of output["results"] as unknown[]) {
    const url = stringField(result, "url");
    if (url !== undefined) sources.push({ url, title: stringField(result, "title") ?? "" });
  }
  return sources;
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

/** 改动类工具（播放控制、收藏、歌单与历史的写入、曲库与艺人的治理）；只读工具与未登记的工具不算。 */
export function toolWritesOf(name: string): boolean {
  const known = toolLabels[name];
  return known?.writes === true || known?.changes !== undefined;
}

/** 这个工具会改动哪类数据；只读与播放类工具是 null。 */
export function toolDataScopeOf(name: string): ToolDataScope | null {
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
  // 目标在读取之后被改过（版本号对不上），需要重新读取。
  revision_conflict: "内容已被改过",
  // C6：方案里用户没有勾选的项；用户发了新消息、方案作废时没有执行的项。
  declined_by_user: "你没有选这一项",
  not_executed: "没有执行",
  // C7：被审查拒绝的项；审查不可用时没有执行的项。
  rejected_by_review:"审查没有通过",
  review_unavailable: "审查不可用",
  queue_full: "队列已满",
  volume_fixed: "这个设备的音量不能调",
  playlist_limit_reached: "歌单数量已到上限",
  playlist_track_limit_reached: "歌单曲目已到上限",
  no_playable_tracks: "没有可播放的曲目",
  // C8：联网搜索、读取网页与音乐资料库。
  search_not_configured: "没有配置搜索",
  searxng_json_disabled: "SearXNG 没有开启 JSON 输出",
  search_key_invalid: "搜索密钥不对",
  search_quota_exceeded: "搜索额度用完了",
  search_unreachable: "连不上搜索服务",
  egress_denied: "地址不允许连接",
  fetch_blocked_address: "不能访问这个地址",
  fetch_blocked_port: "只能访问 80 和 443 端口",
  fetch_unsupported_type: "不是文字网页",
  fetch_too_many_redirects: "跳转太多次",
  fetch_timeout: "超时",
  fetch_not_found: "网页不存在",
  fetch_forbidden: "网站拒绝访问",
  fetch_failed: "没能读取",
  rate_limited: "请求太频繁",
  source_unavailable: "资料来源暂时不可用",
};

/** 失败的工具调用右侧的短语。 */
export function toolFailureOf(errorCode: string | null): string {
  return (errorCode === null ? undefined : failurePhrases[errorCode]) ?? "失败";
}
