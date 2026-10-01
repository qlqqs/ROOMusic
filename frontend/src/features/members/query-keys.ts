import { viewerQueryKey } from "../auth";

export const memberKeys = {
  list: (generation: number) => viewerQueryKey(generation, "members", "list"),
};
