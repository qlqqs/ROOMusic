import { viewerQueryKey } from "../auth";

export const libraryKeys = {
  roots: (generation: number) => viewerQueryKey(generation, "library", "roots"),
  rootOperations: (generation: number) => viewerQueryKey(generation, "library", "root-operations"),
  activeScan: (generation: number) => viewerQueryKey(generation, "library", "scans", "active"),
  /** 扫描记录的全部页：启动扫描或扫描结束时整体失效。 */
  scanHistory: (generation: number) => viewerQueryKey(generation, "library", "scans", "history"),
  scanHistoryPage: (generation: number, page: number) => viewerQueryKey(generation, "library", "scans", "history", page),
  scan: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId),
  diagnostics: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId, "diagnostics"),
};
