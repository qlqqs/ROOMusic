import { useCallback, type ReactNode } from "react";
import { SearchResultsView, type SearchPageSearch, type SearchTrackItemDTO } from "../../features/search";
import { SearchPlaybackNotice, SearchTrackActions } from "./search-playback";
import { TrackFavoritesScope } from "./track-favorites-scope";

/**
 * 全局搜索的结果页：search 给出两栏结果与分页；曲目行的播放、心形与“更多”由这里经插槽组合，
 * 这一页曲目的收藏状态一次读出（包住曲目表格的范围，翻页、换词时随这一页的曲目更新）。查询词与页码来自地址（路由已校验）。
 */
export function SearchPage({ search }: { search: SearchPageSearch }) {
  // 插槽保持同一个函数引用，表格的列定义才不会每次重建。
  const renderTrackActions = useCallback((item: SearchTrackItemDTO) => <SearchTrackActions item={item} />, []);
  const renderTrackRowsScope = useCallback((items: readonly SearchTrackItemDTO[], children: ReactNode) => <TrackFavoritesScope tracks={items}>{children}</TrackFavoritesScope>, []);
  return <SearchResultsView search={search} renderTrackActions={renderTrackActions} renderTrackRowsScope={renderTrackRowsScope} tracksNotice={<SearchPlaybackNotice />} />;
}
