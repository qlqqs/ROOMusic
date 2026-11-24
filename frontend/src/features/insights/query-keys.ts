import { viewerQueryKey } from "../auth";

export const insightsKeys = {
  status: (generation: number) => viewerQueryKey(generation, "insights", "status"),
  about: (generation: number) => viewerQueryKey(generation, "insights", "about"),
};
