import { useCallback } from "react";
import { SearchResultsView, type SearchPageSearch, type SearchTrackItemDTO } from "../../features/search";
import { SearchPlaybackNotice, SearchTrackActions } from "./search-playback";

/**
 * 全局搜索的结果页：search 给出两栏结果与分页，曲目行的播放动作由这里经插槽组合播放器。查询词与页码来自地址（路由已校验）。
 */
export function SearchPage({ search }: { search: SearchPageSearch }) {
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderTrackActions = useCallback((item: SearchTrackItemDTO) => <SearchTrackActions item={item} />, []);
  return <SearchResultsView search={search} renderTrackActions={renderTrackActions} tracksNotice={<SearchPlaybackNotice />} />;
}
