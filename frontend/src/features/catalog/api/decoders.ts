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

const artworkDecoder = d.object({
  resource_id: d.string({ min: 1, max: 255 }),
  mime: d.string({ min: 1, max: 100 }),
  width: d.integer({ min: 1, max: 100_000 }),
  height: d.integer({ min: 1, max: 100_000 }),
});

// 摘要：列表里的代表版本、组详情里的成员、发行版本详情的头部共用同一个形状。
const releaseSummaryShape = {
  id: d.uuid(),
  title: d.string(),
  artist: d.string(),
  album_artist: d.nullable(d.string()),
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

const releaseGroupSummaryShape = {
  id: d.uuid(),
  release_count: d.integer({ min: 1 }),
  attention_count: d.integer({ min: 0 }),
  representative: releaseSummaryDecoder,
};
const releaseGroupSummaryDecoder = d.object(releaseGroupSummaryShape);

const releaseGroupPageDecoder = d.object({
  items: d.array(releaseGroupSummaryDecoder, { max: MAX_PAGE_ITEMS }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: MAX_PAGE_ITEMS }),
    total: d.integer({ min: 0 }),
  }),
});

// 组详情：摘要加成员摘要（至少一个可见成员，最多 100 条；release_count 是完整数量，可以大于 releases 的长度）。
const releaseGroupDetailDecoder = d.object({
  ...releaseGroupSummaryShape,
  releases: d.array(releaseSummaryDecoder, { min: 1, max: MAX_GROUP_RELEASES }),
});

// 署名的角色与证据里的各种词表都按字符串解码：后端新增取值不会让页面解码失败，显示时由 labels 给已知值中文标签。
const creditDecoder = d.object({
  role: d.string({ min: 1, max: 64 }),
  name: d.string(),
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

export const decodeReleaseGroupPage = d.defineDecoder(releaseGroupPageDecoder);
export const decodeReleaseGroupDetail = d.defineDecoder(releaseGroupDetailDecoder);
export const decodeReleaseDetail = d.defineDecoder(releaseDetailDecoder);
export const decodeReleaseEvidence = d.defineDecoder(releaseEvidenceDecoder);

export type ArtworkDTO = d.Infer<typeof artworkDecoder>;
export type ReleaseSummaryDTO = d.Infer<typeof releaseSummaryDecoder>;
export type ReleaseGroupSummaryDTO = d.Infer<typeof releaseGroupSummaryDecoder>;
export type ReleaseGroupPageDTO = d.Infer<typeof releaseGroupPageDecoder>;
export type ReleaseGroupDetailDTO = d.Infer<typeof releaseGroupDetailDecoder>;
export type ReleaseDetailDTO = d.Infer<typeof releaseDetailDecoder>;
export type ReleaseMediumDTO = d.Infer<typeof mediumDecoder>;
export type ReleaseTrackDTO = d.Infer<typeof trackDecoder>;
export type ReleaseCreditDTO = d.Infer<typeof creditDecoder>;
export type ReleaseCueDTO = d.Infer<typeof cueDecoder>;
export type ReleaseEvidenceSummaryDTO = d.Infer<typeof evidenceSummaryDecoder>;
export type ReleaseEvidenceDTO = d.Infer<typeof releaseEvidenceDecoder>;
export type ReleaseFieldEvidenceDTO = d.Infer<typeof fieldEvidenceDecoder>;
export type ReleaseGroupingEvidenceDTO = d.Infer<typeof groupingEvidenceDecoder>;
