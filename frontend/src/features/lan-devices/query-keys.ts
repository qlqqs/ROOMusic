import { viewerQueryKey } from "../auth";

export const lanDeviceKeys = {
  all: (generation: number) => viewerQueryKey(generation, "lan-devices"),
  settings: (generation: number) => viewerQueryKey(generation, "lan-devices", "settings"),
  devices: (generation: number) => viewerQueryKey(generation, "lan-devices", "devices"),
};
