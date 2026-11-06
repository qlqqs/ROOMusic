import { viewerQueryKey } from "../auth";

export const systemKeys = {
  backups: (generation: number) => viewerQueryKey(generation, "system", "backups"),
  version: (generation: number) => viewerQueryKey(generation, "system", "version"),
};
