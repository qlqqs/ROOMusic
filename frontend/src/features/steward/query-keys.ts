import { viewerQueryKey } from "../auth";

export const stewardKeys = {
  tokens: (generation: number) => viewerQueryKey(generation, "steward", "tokens"),
  settings: (generation: number) => viewerQueryKey(generation, "steward", "settings"),
  conversations: (generation: number) => viewerQueryKey(generation, "steward", "conversations"),
  conversation: (generation: number, id: string) => viewerQueryKey(generation, "steward", "conversation", id),
  instance: (generation: number) => viewerQueryKey(generation, "steward", "instance"),
  allowlist: (generation: number) => viewerQueryKey(generation, "steward", "allowlist"),
};
