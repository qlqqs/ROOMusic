import { viewerQueryKey } from "../auth";

export const stewardKeys = {
  tokens: (generation: number) => viewerQueryKey(generation, "steward", "tokens"),
};
