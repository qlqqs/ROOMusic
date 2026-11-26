import { viewerQueryKey } from "../auth";

export const curationKeys = {
  all: (generation: number) => viewerQueryKey(generation, "curation"),
  settings: (generation: number) => viewerQueryKey(generation, "curation", "settings"),
  sources: (generation: number) => viewerQueryKey(generation, "curation", "sources"),
  progress: (generation: number) => viewerQueryKey(generation, "curation", "progress"),
  /** “整理艺人”全量队列的状态。 */
  artistWebQueue: (generation: number) => viewerQueryKey(generation, "curation", "artist-web-queue"),
  /** 一张发行在各来源的识别结论（专辑详情的管理员区块）。 */
  matchResults: (generation: number, releaseId: string) => viewerQueryKey(generation, "curation", "match-results", releaseId),
  classifications: (generation: number) => viewerQueryKey(generation, "curation", "classifications"),
  evidenceCount: (generation: number, provider: string) => viewerQueryKey(generation, "curation", "evidence-count", provider),
  batches: (generation: number) => viewerQueryKey(generation, "curation", "batches"),
  batchPage: (generation: number, page: number) => viewerQueryKey(generation, "curation", "batches", page),
  suppressions: (generation: number) => viewerQueryKey(generation, "curation", "suppressions"),
  suppressionPage: (generation: number, page: number) => viewerQueryKey(generation, "curation", "suppressions", page),
  exceptions: (generation: number) => viewerQueryKey(generation, "curation", "exceptions"),
  exceptionPage: (generation: number, page: number) => viewerQueryKey(generation, "curation", "exceptions", page),
  conversations: (generation: number) => viewerQueryKey(generation, "curation", "conversations"),
  plugins: (generation: number) => viewerQueryKey(generation, "curation", "plugins"),
};
