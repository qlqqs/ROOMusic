import { viewerQueryKey } from "../auth";

export const libraryKeys = {
  roots: (generation: number) => viewerQueryKey(generation, "library", "roots"),
  activeScan: (generation: number) => viewerQueryKey(generation, "library", "scans", "active"),
};
