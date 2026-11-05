// search 的公开入口：侧栏的全局搜索框、结果页内容（曲目动作经插槽由 app 组合）、结果页的 URL 校验、曲目结果的类型，
// 以及查询键（app 在 epoch 变化、修改专辑数据后让搜索失效或取消旧代次的请求）。外部组件不导入 feature 内部文件。
export { GLOBAL_SEARCH_INPUT_SELECTOR, GlobalSearchBox } from "./components/global-search-box";
export { SearchResultsView } from "./components/search-results-view";
export { parseSearchPageSearch, type SearchPageSearch } from "./search-params";
export type { SearchArtistItemDTO, SearchTrackItemDTO } from "./api/decoders";
export { epochOfSearchKey, searchKeys } from "./query-keys";
