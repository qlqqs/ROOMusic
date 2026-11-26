import type { ArtistWebSearchDTO } from "../features/artists/api/decoders";

/** 联网查找状态的替身：默认没有配网页搜索、不在队列里，上限同接口样本；按需覆盖。单元测试与 e2e 共用，不依赖 vitest。 */
export function artistWebSearchOf(patch: Partial<ArtistWebSearchDTO> = {}): ArtistWebSearchDTO {
  return {
    state: "none",
    candidates: [],
    configured: false,
    can_start: false,
    can_stop: false,
    queue: null,
    usage: { tool_calls: 0, tokens: 0, tool_call_limit: 10, token_limit: 50_000 },
    ...patch,
  };
}
