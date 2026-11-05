import { viewerQueryKey } from "../auth";
import type { ArtistListParams } from "./api/endpoints";
import type { ArtistTrackRole } from "./api/decoders";

// g 是会话代次，e 是目录状态的 epoch（艺人数据的代次）：艺人数据发布或重新整理后 epoch 变大，旧代次的结果留在旧键下。
export const artistsKeys = {
  /** 艺人的全部查询：治理（合并、拆开、改名）之后、epoch 变化后整体失效。 */
  all: (generation: number) => viewerQueryKey(generation, "artists"),
  page: (generation: number, epoch: number, params: ArtistListParams) =>
    viewerQueryKey(generation, "artists", "list", { e: epoch, q: params.q ?? null, initial: params.initial ?? null, role: params.role ?? null, sort: params.sort, page: params.page, pageSize: params.pageSize }),
  /** 全部艺人详情（前缀）：治理之后移除，旧的重定向结果不能再把人送走。 */
  details: (generation: number) => viewerQueryKey(generation, "artists", "detail"),
  detail: (generation: number, epoch: number, artistId: string) => viewerQueryKey(generation, "artists", "detail", artistId, { e: epoch }),
  names: (generation: number, epoch: number, artistId: string, page: number, pageSize: number) => viewerQueryKey(generation, "artists", "names", artistId, { e: epoch, page, pageSize }),
  albums: (generation: number, epoch: number, artistId: string, page: number, pageSize: number) => viewerQueryKey(generation, "artists", "albums", artistId, { e: epoch, page, pageSize }),
  participationAlbums: (generation: number, epoch: number, artistId: string, q: string | null, page: number, pageSize: number) =>
    viewerQueryKey(generation, "artists", "albums-any", artistId, { e: epoch, q, page, pageSize }),
  tracks: (generation: number, epoch: number, artistId: string, role: ArtistTrackRole, page: number, pageSize: number) =>
    viewerQueryKey(generation, "artists", "tracks", artistId, { e: epoch, role, page, pageSize }),
  queue: (generation: number, artistId: string, order: string, seed: string | null) => viewerQueryKey(generation, "artists", "queue", artistId, { order, seed }),
};
