import { viewerQueryKey } from "../auth";

export const playerKeys = {
  formats: (generation: number) => viewerQueryKey(generation, "player", "formats"),
  /** 当前会话全部曲目的歌词：自动补的歌词被撤销之后由 app 用它整体失效。 */
  allLyrics: (generation: number) => viewerQueryKey(generation, "player", "lyrics"),
  /** 一首曲目的歌词：会话内不过期；会话变化随代次一起清空。 */
  lyrics: (generation: number, trackId: string) => viewerQueryKey(generation, "player", "lyrics", trackId),
  /** 一首曲目的音频分析：完成的结果会话内不过期，还没完成或失败的 5 分钟后再读；波形与信号路径共用。 */
  analysis: (generation: number, trackId: string) => viewerQueryKey(generation, "player", "analysis", trackId),
  opraSearch: (generation: number, query: string) => viewerQueryKey(generation, "player", "opra", "search", query),
  opraProduct: (generation: number, productId: string) => viewerQueryKey(generation, "player", "opra", "product", productId),
  peqResponse: (generation: number, body: string) => viewerQueryKey(generation, "player", "peq", "response", body),
  peqImport: (generation: number, text: string) => viewerQueryKey(generation, "player", "peq", "import", text),
  peqPresets: (generation: number) => viewerQueryKey(generation, "player", "peq", "presets"),
  irFiles: (generation: number) => viewerQueryKey(generation, "player", "ir-files"),
};
