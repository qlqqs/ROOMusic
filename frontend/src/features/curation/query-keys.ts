import { viewerQueryKey } from "../auth";

export const curationKeys = {
  all: (generation: number) => viewerQueryKey(generation, "curation"),
  settings: (generation: number) => viewerQueryKey(generation, "curation", "settings"),
  sources: (generation: number) => viewerQueryKey(generation, "curation", "sources"),
  progress: (generation: number) => viewerQueryKey(generation, "curation", "progress"),
  batches: (generation: number) => viewerQueryKey(generation, "curation", "batches"),
  batchPage: (generation: number, page: number) => viewerQueryKey(generation, "curation", "batches", page),
  suppressions: (generation: number) => viewerQueryKey(generation, "curation", "suppressions"),
  suppressionPage: (generation: number, page: number) => viewerQueryKey(generation, "curation", "suppressions", page),
};
