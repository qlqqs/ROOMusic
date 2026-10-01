import { viewerQueryKey } from "../auth";

export const libraryKeys = {
  roots: (generation: number) => viewerQueryKey(generation, "library", "roots"),
  rootOperations: (generation: number) => viewerQueryKey(generation, "library", "root-operations"),
  activeScan: (generation: number) => viewerQueryKey(generation, "library", "scans", "active"),
  scan: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId),
  diagnostics: (generation: number, scanId: string) => viewerQueryKey(generation, "library", "scans", scanId, "diagnostics"),
};
