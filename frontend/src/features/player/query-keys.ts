import { viewerQueryKey } from "../auth";

export const playerKeys = {
  formats: (generation: number) => viewerQueryKey(generation, "player", "formats"),
  capabilities: (generation: number) => viewerQueryKey(generation, "player", "capabilities"),
};
