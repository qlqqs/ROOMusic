import { decode as d } from "../../../shared/api";

const artworkDecoder = d.object({
  resource_id: d.string({ min: 1, max: 255 }),
  mime: d.string({ min: 1, max: 100 }),
  width: d.integer({ min: 1, max: 100_000 }),
  height: d.integer({ min: 1, max: 100_000 }),
});

// 代表版本的摘要；M1a 的概览只用列表总数，M1b 的专辑列表与详情复用这里的合同。
const releaseSummaryDecoder = d.object({
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
});

const releaseGroupSummaryDecoder = d.object({
  id: d.uuid(),
  release_count: d.integer({ min: 1 }),
  attention_count: d.integer({ min: 0 }),
  representative: releaseSummaryDecoder,
});

// 每页最多 100 个组（后端 maxReleaseRows）。
const releaseGroupPageDecoder = d.object({
  items: d.array(releaseGroupSummaryDecoder, { max: 100 }),
  pagination: d.object({
    page: d.integer({ min: 1 }),
    page_size: d.integer({ min: 1, max: 100 }),
    total: d.integer({ min: 0 }),
  }),
});

export const decodeReleaseGroupPage = d.defineDecoder(releaseGroupPageDecoder);
export type ReleaseSummaryDTO = d.Infer<typeof releaseSummaryDecoder>;
export type ReleaseGroupSummaryDTO = d.Infer<typeof releaseGroupSummaryDecoder>;
export type ReleaseGroupPageDTO = d.Infer<typeof releaseGroupPageDecoder>;
