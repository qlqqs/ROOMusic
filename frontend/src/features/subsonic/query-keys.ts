import { viewerQueryKey } from "../auth";

export const subsonicKeys = {
  connection: (generation: number) => viewerQueryKey(generation, "subsonic", "connection"),
  settings: (generation: number) => viewerQueryKey(generation, "subsonic", "settings"),
};
