import { decode as d } from "../../../shared/api";

// 上限与后端一致（backend/internal/catalog/read.go、groups.go）：超界的响应整体拒绝，而不是截断后显示。
const MAX_PAGE_ITEMS = 100; // 每页最多 100 个组（maxReleaseRows）
const MAX_GROUP_RELEASES = 100; // 组详情的成员摘要（maxReleaseGroupMembers）
const MAX_MEDIA = 256;
const MAX_RELEASE_TRACKS = 10_000; // 整张发行版本合计，不是每个介质各一份
const MAX_RELEASE_CREDITS = 100;
const MAX_TRACK_CREDITS = 100;
const MAX_RELEASE_TRACK_CREDITS = 10_000; // 整张发行版本所有曲目的署名合计
const MAX_RELEASE_EVIDENCE = 100;
const MAX_EVIDENCE_CANDIDATES = 20;
const MAX_EVIDENCE_SOURCE_REFS = 100;
const MAX_EVIDENCE_TEXT = 500; // 证据里的值、候选、原因码与来源引用，后端按 500 字节截断
const MAX_CORRECTED_FIELDS = 16; // 有人工修正的字段名；后端目前可修正 8 个字段，留出余量
const MAX_COMPARED_FORMATS = 32; // 版本对比里一个版本的不同音频参数组合（maxComparedFormats）

// 别的 feature（歌单卡片的封面拼图）经这个模块复用同一个封面摘要解码器。
export const artworkDecoder = d.object({
  resource_id: d.string({ min: 1, max: 255 }),
  mime: d.string({ min: 1, max: 100 }),
  width: d.integer({ min: 1, max: 100_000 }),
  height: d.integer({ min: 1, max: 100_000 }),
});

/** 一个署名最多对应的艺人引用数（与后端一致）。 */
export const MAX_ARTIST_REFS = 100;

// 署名对应的艺人（M5 ⑤）：artist_id 是有效艺人，name 是这处署名的原写法（链接文字用它），display_name 是艺人的主显示名。
// 艺人数据还在整理时后端给空数组，文字与播放事实照常；前端只消费这些明确的 ID，不按同名字符串猜人。
export const artistRefDecoder = d.object({
  artist_id: d.uuid(),
  name: d.string(),
  display_name: d.string(),
});
export const artistRefsDecoder = d.array(artistRefDecoder, { max: MAX_ARTIST_REFS });

// 摘要：列表里的代表版本、组详情里的成员、发行版本详情的头部共用同一个形状。
const releaseSummaryShape = {
  id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  album_artist: d.nullable(d.string()),
  artist_refs: artistRefsDecoder,
  album_artist_refs: artistRefsDecoder,
  year: d.nullable(d.integer({ min: 1, max: 9999 })),
  source_type: d.nullable(d.string()),
  media_type: d.nullable(d.string()),
  medium_count: d.integer({ min: 0 }),
  track_count: d.integer({ min: 0 }),
  attention_count: d.integer({ min: 0 }),
  artwork: d.nullable(artworkDecoder),
};
// 其他 feature（最近播放）经 catalog 的公开入口复用同一个摘要解码器，保证两处对封面等字段的校验一致。
export const releaseSummaryDecoder = d.object(releaseSummaryShape);

// added_at 是专辑的加入时间：组内当前可见版本里最早的创建时间（不是代表版本的时间，归组、认领后可能变化）。
// 旧服务没有这个字段，普通列表把它当可选；按加入时间排序与“最近新增”必须有它（见下方的 WithAddedAt）。
const releaseGroupSummaryShape = {
  id: d.uuid(),
  release_count: d.integer({ min: 1 }),
  attention_count: d.integer({ min: 0 }),
  representative: releaseSummaryDecoder,
  added_at: d.optional(d.timestamp()),
};
// 全局搜索的专辑结果（features/search）复用同一个组摘要解码器。
export const releaseGroupSummaryDecoder = d.object(releaseGroupSummaryShape);

// 必须带加入时间的组摘要：“最近新增”、按加入时间排序的列表与扫描报告里的新增专辑用它。
// 旧服务会忽略 sort 参数而按标题返回，缺少 added_at 时整体拒绝，不把按标题排列的结果当成“最近新增”显示。
const releaseGroupSummaryWithAddedAtShape = { ...releaseGroupSummaryShape, added_at: d.timestamp() };
// 扫描报告（library）经 api/decoders 模块直接复用它，保证两处对专辑卡片字段的校验一致。
export const releaseGroupSummaryWithAddedAtDecoder = d.object(releaseGroupSummaryWithAddedAtShape);

const paginationDecoder = d.object({
  page: d.integer({ min: 1 }),
  page_size: d.integer({ min: 1, max: MAX_PAGE_ITEMS }),
  total: d.integer({ min: 0 }),
});

const releaseGroupPageDecoder = d.object({
  items: d.array(releaseGroupSummaryDecoder, { max: MAX_PAGE_ITEMS }),
  pagination: paginationDecoder,
});

const releaseGroupAddedPageDecoder = d.object({
  items: d.array(releaseGroupSummaryWithAddedAtDecoder, { max: MAX_PAGE_ITEMS }),
  pagination: paginationDecoder,
});

/** “最近新增”固定最多 8 项，按加入时间倒序。 */
export const MAX_RECENTLY_ADDED = 8;
const recentlyAddedDecoder = d.object({ items: d.array(releaseGroupSummaryWithAddedAtDecoder, { max: MAX_RECENTLY_ADDED }) });

// 组详情：摘要加成员摘要（至少一个可见成员，最多 100 条；release_count 是完整数量，可以大于 releases 的长度）。
// revision 是专辑的人工修改版本号（设定默认版本与撤销共用；管理员写操作用它做预期版本）。default_release_id 只在管理员指定的
// 默认版本正在生效时才有值，此时它就是 releases 的第一项；没有指定、指定的版本不可见或已被移出时为 null（专辑回到自动规则）。
const releaseGroupDetailDecoder = d.object({
  ...releaseGroupSummaryShape,
  revision: d.integer({ min: 1 }),
  default_release_id: d.nullable(d.uuid()),
  releases: d.array(releaseSummaryDecoder, { min: 1, max: MAX_GROUP_RELEASES }),
});

// 版本对比：专辑各个可见版本并排比较的发行字段（有效值）与内容。成员顺序与组详情一致（生效中的默认版本在前，最多 100 个，
// release_count 是完整数量）。介质、曲目、时长与格式只计当前存在的曲目；总时长只在每一首曲目的时长都已知时才有值，否则为 null；
// 格式按曲目数降序，未知的参数为 null。这里只如实列出参数，不含任何音质判断。
const audioFormatDecoder = d.object({
  codec: d.nullable(d.string()),
  bit_depth: d.nullable(d.integer({ min: 1 })),
  sample_rate: d.nullable(d.integer({ min: 1 })),
  track_count: d.integer({ min: 1 }),
});

const comparedReleaseDecoder = d.object({
  id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  album_artist: d.nullable(d.string()),
  year: d.nullable(d.integer({ min: 1, max: 9999 })),
  edition: d.nullable(d.string()),
  label: d.nullable(d.string()),
  catalog_number: d.nullable(d.string()),
  barcode: d.nullable(d.string()),
  genre: d.nullable(d.string()),
  source_type: d.nullable(d.string()),
  media_type: d.nullable(d.string()),
  medium_count: d.integer({ min: 0 }),
  track_count: d.integer({ min: 0 }),
  duration_seconds: d.nullable(d.number({ min: 0 })),
  formats: d.array(audioFormatDecoder, { max: MAX_COMPARED_FORMATS }),
  // 字段名按字符串解码（与发行详情一致）：后端新增可修正字段不会让对比失败。
  corrected_fields: d.array(d.string({ min: 1, max: 64 }), { max: MAX_CORRECTED_FIELDS }),
});

const releaseComparisonDecoder = d.object({
  group_id: d.uuid(),
  default_release_id: d.nullable(d.uuid()),
  release_count: d.integer({ min: 1 }),
  releases: d.array(comparedReleaseDecoder, { min: 1, max: MAX_GROUP_RELEASES }),
});

// 署名的角色与证据里的各种词表都按字符串解码：后端新增取值不会让页面解码失败，显示时由 labels 给已知值中文标签。
const creditDecoder = d.object({
  role: d.string({ min: 1, max: 64 }),
  name: d.string(),
  artist_refs: artistRefsDecoder,
});

// CUE 分轨的位置：帧数（每秒 75 帧）与折算出的秒数，字段都可能缺失。
const cueDecoder = d.object({
  index_frames: d.nullable(d.integer({ min: 0 })),
  end_frames: d.nullable(d.integer({ min: 0 })),
  start_seconds: d.nullable(d.number({ min: 0 })),
  end_seconds: d.nullable(d.number({ min: 0 })),
  isrc: d.nullable(d.string()),
});

const trackDecoder = d.object({
  id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  artist_refs: artistRefsDecoder,
  position: d.integer({ min: 0 }),
  // 只是文件名（或 CUE 分轨的标签），后端不返回路径。
  source: d.string(),
  source_kind: d.string({ min: 1 }),
  duration_seconds: d.nullable(d.number({ min: 0 })),
  codec: d.nullable(d.string()),
  bit_depth: d.nullable(d.integer({ min: 0 })),
  sample_rate: d.nullable(d.integer({ min: 0 })),
  channels: d.nullable(d.integer({ min: 0 })),
  bitrate: d.nullable(d.integer({ min: 0 })),
  cue: d.nullable(cueDecoder),
  credits: d.array(creditDecoder, { max: MAX_TRACK_CREDITS }),
});

const mediumDecoder = d.object({
  id: d.uuid(),
  position: d.integer({ min: 0 }),
  title: d.string(),
  tracks: d.array(trackDecoder, { max: MAX_RELEASE_TRACKS }),
});

function isRecord(input: unknown): input is Record<string, unknown> {
  return typeof input === "object" && input !== null && !Array.isArray(input);
}

// 介质列表。曲目与曲目署名的上限是整张发行版本的合计：在解码每个介质之前先累计，超界就在分配这一批曲目之前失败，
// 而不是每个介质各给 10,000 条的额度。
const mediaDecoder: d.Decoder<d.Infer<typeof mediumDecoder>[]> = (input, path) => {
  if (!Array.isArray(input)) throw new d.DecodeError(path, "应为数组");
  if (input.length > MAX_MEDIA) throw new d.DecodeError(path, `项数应在 0 到 ${MAX_MEDIA} 之间`);
  let tracks = 0;
  let credits = 0;
  return (input as unknown[]).map((medium, index) => {
    const rawTracks = isRecord(medium) ? medium["tracks"] : undefined;
    if (Array.isArray(rawTracks)) {
      tracks += rawTracks.length;
      if (tracks > MAX_RELEASE_TRACKS) {
        throw new d.DecodeError(`${path}[${index}].tracks`, `整张发行版本的曲目合计不应超过 ${MAX_RELEASE_TRACKS}`);
      }
      for (const rawTrack of rawTracks as unknown[]) {
        const rawCredits = isRecord(rawTrack) ? rawTrack["credits"] : undefined;
        if (Array.isArray(rawCredits)) credits += rawCredits.length;
      }
      if (credits > MAX_RELEASE_TRACK_CREDITS) {
        throw new d.DecodeError(`${path}[${index}].tracks`, `整张发行版本的曲目署名合计不应超过 ${MAX_RELEASE_TRACK_CREDITS}`);
      }
    }
    return mediumDecoder(medium, `${path}[${index}]`);
  });
};

// 证据摘要：所有登录用户都能读；字段、来源、可信度与处理方式都是后端的词表，按字符串解码。
const evidenceSummaryShape = {
  field: d.string({ min: 1 }),
  source: d.string(),
  confidence: d.string(),
  action: d.string(),
  rule_id: d.string(),
};
const evidenceSummaryDecoder = d.object(evidenceSummaryShape);

const releaseDetailDecoder = d.object({
  ...releaseSummaryShape,
  candidate_kind: d.string({ min: 1 }),
  genre: d.nullable(d.string()),
  catalog_number: d.nullable(d.string()),
  edition: d.nullable(d.string()),
  label: d.nullable(d.string()),
  barcode: d.nullable(d.string()),
  media: mediaDecoder,
  credits: d.array(creditDecoder, { max: MAX_RELEASE_CREDITS }),
  evidence: d.array(evidenceSummaryDecoder, { max: MAX_RELEASE_EVIDENCE }),
  // 人工修改版本号（修正、归组与撤销共用；管理员写操作用它做预期版本）与有人工修正的字段名。字段名按字符串解码：
  // 后端新增可修正字段不会让页面解码失败，显示时由 labels 给已知值中文标签。
  revision: d.integer({ min: 1 }),
  corrected_fields: d.array(d.string({ min: 1, max: 64 }), { max: MAX_CORRECTED_FIELDS }),
});

// 管理员证据里的来源引用必须是有界的安全相对路径：绝对路径、盘符、`file://`、反斜杠与上级目录一律拒绝。
// 后端返回的是 path.Clean 之后的值，所以空段与 "." 段也不会出现。
const sourceRefDecoder: d.Decoder<string> = (input, path) => {
  const value = d.string({ min: 1, max: MAX_EVIDENCE_TEXT })(input, path);
  const segments = value.split("/");
  const unsafe =
    value.startsWith("/") ||
    value.includes("\\") ||
    value.includes("\0") ||
    (segments[0] ?? "").includes(":") ||
    segments.some((segment) => segment === "" || segment === "." || segment === "..");
  if (unsafe) throw new d.DecodeError(path, "来源引用应为安全的相对路径");
  return value;
};

const fieldEvidenceDecoder = d.object({
  ...evidenceSummaryShape,
  value: d.nullable(d.string({ max: MAX_EVIDENCE_TEXT })),
  candidates: d.array(d.string({ max: MAX_EVIDENCE_TEXT }), { max: MAX_EVIDENCE_CANDIDATES }),
  reason_code: d.nullable(d.string({ max: MAX_EVIDENCE_TEXT })),
});

const groupingEvidenceDecoder = d.object({
  candidate_kind: d.string({ min: 1 }),
  rule_id: d.string(),
  source_refs: d.array(sourceRefDecoder, { max: MAX_EVIDENCE_SOURCE_REFS }),
  reason_codes: d.array(d.string({ max: MAX_EVIDENCE_TEXT }), { max: MAX_EVIDENCE_CANDIDATES }),
});

const releaseEvidenceDecoder = d.object({
  release_id: d.uuid(),
  fields: d.array(fieldEvidenceDecoder, { max: MAX_RELEASE_EVIDENCE }),
  grouping: d.nullable(groupingEvidenceDecoder),
  truncated: d.boolean(),
});

// 目录状态（GET /catalog/status，搜索响应里也带同一快照）：epoch 是不透明的单调整数，变大说明搜索或艺人数据换了一代；
// search_mode 为 legacy 时新搜索还在后台准备，搜索照常可用，只是匹配方式是旧的。
export const catalogStateDecoder = d.object({
  epoch: d.integer({ min: 0 }),
  search_mode: d.oneOf(["legacy", "normalized"]),
  artists_state: d.oneOf(["unavailable", "preparing", "ready"]),
});

export const decodeReleaseGroupPage = d.defineDecoder(releaseGroupPageDecoder);
export const decodeReleaseGroupAddedPage = d.defineDecoder(releaseGroupAddedPageDecoder);
export const decodeRecentlyAdded = d.defineDecoder(recentlyAddedDecoder);
export const decodeReleaseGroupDetail = d.defineDecoder(releaseGroupDetailDecoder);
export const decodeReleaseDetail = d.defineDecoder(releaseDetailDecoder);
export const decodeReleaseComparison = d.defineDecoder(releaseComparisonDecoder);
export const decodeReleaseEvidence = d.defineDecoder(releaseEvidenceDecoder);
export const decodeCatalogState = d.defineDecoder(catalogStateDecoder);

export type ArtworkDTO = d.Infer<typeof artworkDecoder>;
export type ArtistRefDTO = d.Infer<typeof artistRefDecoder>;
export type ReleaseSummaryDTO = d.Infer<typeof releaseSummaryDecoder>;
export type ReleaseGroupSummaryDTO = d.Infer<typeof releaseGroupSummaryDecoder>;
export type ReleaseGroupPageDTO = d.Infer<typeof releaseGroupPageDecoder>;
export type ReleaseGroupSummaryWithAddedAtDTO = d.Infer<typeof releaseGroupSummaryWithAddedAtDecoder>;
export type RecentlyAddedDTO = d.Infer<typeof recentlyAddedDecoder>;
export type ReleaseGroupDetailDTO = d.Infer<typeof releaseGroupDetailDecoder>;
export type ReleaseDetailDTO = d.Infer<typeof releaseDetailDecoder>;
export type ReleaseComparisonDTO = d.Infer<typeof releaseComparisonDecoder>;
export type ComparedReleaseDTO = d.Infer<typeof comparedReleaseDecoder>;
export type AudioFormatDTO = d.Infer<typeof audioFormatDecoder>;
export type ReleaseMediumDTO = d.Infer<typeof mediumDecoder>;
export type ReleaseTrackDTO = d.Infer<typeof trackDecoder>;
export type ReleaseCreditDTO = d.Infer<typeof creditDecoder>;
export type ReleaseCueDTO = d.Infer<typeof cueDecoder>;
export type ReleaseEvidenceSummaryDTO = d.Infer<typeof evidenceSummaryDecoder>;
export type ReleaseEvidenceDTO = d.Infer<typeof releaseEvidenceDecoder>;
export type ReleaseFieldEvidenceDTO = d.Infer<typeof fieldEvidenceDecoder>;
export type ReleaseGroupingEvidenceDTO = d.Infer<typeof groupingEvidenceDecoder>;
export type CatalogStateDTO = d.Infer<typeof catalogStateDecoder>;
