import { decodeCatalogState } from "../features/catalog/api/decoders";
import { decodeAlbumResults, decodeTrackResults, type AlbumResultsDTO, type TrackResultsDTO, type SearchPaginationDTO } from "../features/search/api/decoders";
import { loadApiSample, sampleBody } from "./api-samples";

export type SearchResponse = { status: number; body: unknown };
/** 只回应显式登记的查询，不从专辑列表或查询文字推测命中。 */
export type SearchData = { responses: Map<string, SearchResponse> };

export function searchDataOf(): SearchData {
  return { responses: new Map() };
}

/** 将默认参数补齐并排序，使调用端的查询参数顺序不影响登记。 */
export function searchRequestKey(path: string, search: URLSearchParams): string {
  const params = new URLSearchParams();
  params.set("q", search.get("q") ?? "");
  if (path === "/api/v1/search/suggestions") {
    params.set("limit", search.get("limit") ?? "5");
  } else {
    params.set("type", search.get("type") ?? "");
    params.set("page", search.get("page") ?? "1");
    params.set("page_size", search.get("page_size") ?? "50");
  }
  // 不忽略未知参数：错误的调用不能误命中已登记响应。
  for (const [key, value] of search) if (!params.has(key)) params.set(key, value);
  params.sort();
  return `${path}?${params.toString()}`;
}

export function respondToSearch(data: SearchData, path: string, search: URLSearchParams): SearchResponse {
  const response = data.responses.get(searchRequestKey(path, search));
  if (response !== undefined) return structuredClone(response);
  const { status, body } = loadApiSample("errors/not-found-api");
  return { status, body };
}

export const readyCatalogState = () => decodeCatalogState(sampleBody("search/catalog-status-ready"));
export const searchAlbumItem = () => {
  const item = decodeAlbumResults(sampleBody("search/albums")).items[0];
  if (item === undefined) throw new Error("专辑搜索样本没有结果");
  return item;
};
export const searchTrackItem = () => {
  const item = decodeTrackResults(sampleBody("search/tracks")).items[0];
  if (item === undefined) throw new Error("曲目搜索样本没有结果");
  return item;
};

type PageOptions<T> = Partial<Omit<T, "type" | "pagination">> & { pagination?: Partial<SearchPaginationDTO> };
/** 默认一项、精确计数；分页边界场景显式覆盖 pagination。 */
export function searchAlbumPage(options: PageOptions<AlbumResultsDTO> = {}): AlbumResultsDTO {
  const items = options.items ?? [searchAlbumItem()];
  return { query: "爱", catalog_state: readyCatalogState(), ...options, type: "albums", items,
    pagination: { page: 1, page_size: 50, total: items.length, total_relation: "eq", has_more: false, ...options.pagination } };
}
export function searchTrackPage(options: PageOptions<TrackResultsDTO> = {}): TrackResultsDTO {
  const items = options.items ?? [searchTrackItem()];
  return { query: "爱", catalog_state: readyCatalogState(), ...options, type: "tracks", items,
    pagination: { page: 1, page_size: 50, total: items.length, total_relation: "eq", has_more: false, ...options.pagination } };
}
