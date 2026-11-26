import { viewerQueryKey } from "../auth";

export const networkKeys = {
  proxy: (generation: number) => viewerQueryKey(generation, "network", "proxy"),
};
