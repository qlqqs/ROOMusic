import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Page } from "../../../shared/ui";
import type { SearchTrackItemDTO } from "../api/decoders";
import { useCatalogState } from "../../catalog";
import { useAlbumResults, useArtistResults, useTrackResults } from "../hooks/use-search-results";
import type { SearchPageSearch } from "../search-params";
import { AlbumResults } from "./album-results";
import { ArtistResults } from "./artist-results";
import { GlobalSearchBox } from "./global-search-box";
import { ResultSection } from "./result-section";
import { TrackResults } from "./track-results";
import styles from "./search-results-view.module.css";

type SearchResultsViewProps = {
  /** 地址里的查询词与两栏页码（路由已校验）。 */
  search: SearchPageSearch;
  /** 曲目一行的动作：由 app 组合（播放、心形、“更多”）。 */
  renderTrackActions: (item: SearchTrackItemDTO) => ReactNode;
  /** 包住一页曲目行的范围（例如一次读出这一页的收藏状态）：由 app 提供；传入稳定的函数引用。 */
  renderTrackRowsScope?: ((items: readonly SearchTrackItemDTO[], children: ReactNode) => ReactNode) | undefined;
  /** 曲目栏上方的提示（例如无法检查播放能力时的重试）：由 app 提供，没有时不渲染。 */
  tracksNotice?: ReactNode;
};

/** 标题超过这么多个字符时降一级字号（与专辑详情的长标题同一种处理）。 */
const COMPACT_TITLE_LENGTH = 40;

/**
 * 搜索结果页：艺人（就绪时）、专辑、曲目各一栏，各自加载、失败、重试与分页（只改自己那一栏的页码）。
 * 页面里的搜索框只在窄屏出现（宽屏用侧栏的那一个）；没有查询词时任何宽度都显示它。
 */
export function SearchResultsView({ search, renderTrackActions, renderTrackRowsScope, tracksNotice }: SearchResultsViewProps) {
  const q = search.q;
  // 艺人栏只在艺人数据就绪时出现；整理中不出现，也不说明。
  const artistsReady = useCatalogState().data?.artists_state === "ready";
  const title = q === undefined ? "搜索" : `搜索“${q}”`;
  return (
    <Page title={title} compactTitle={title.length > COMPACT_TITLE_LENGTH}>
      <div className={q === undefined ? styles.box : styles.narrowBox}>
        <GlobalSearchBox currentQuery={q} />
      </div>
      {q === undefined ? null : (
        <div className={styles.sections}>
          {artistsReady ? <ArtistSection search={search} q={q} /> : null}
          <AlbumSection search={search} q={q} />
          <TrackSection search={search} q={q} renderTrackActions={renderTrackActions} renderRowsScope={renderTrackRowsScope} notice={tracksNotice} />
        </div>
      )}
    </Page>
  );
}

function ArtistSection({ search, q }: { search: SearchPageSearch; q: string }) {
  const artists = useArtistResults(q, search.artists_page ?? 1, true);
  return (
    <ResultSection
      title="艺人"
      unit="位"
      query={artists}
      emptyText="没有找到匹配的艺人。"
      renderPageLink={(target, props) => <Link to="/search" search={{ ...search, artists_page: target > 1 ? target : undefined }} {...props} />}
    >
      {(items) => <ArtistResults items={items} />}
    </ResultSection>
  );
}

function AlbumSection({ search, q }: { search: SearchPageSearch; q: string }) {
  const albums = useAlbumResults(q, search.albums_page ?? 1);
  return (
    <ResultSection
      title="专辑"
      unit="张"
      query={albums}
      emptyText="没有找到匹配的专辑。"
      renderPageLink={(target, props) => <Link to="/search" search={{ ...search, albums_page: target > 1 ? target : undefined }} {...props} />}
    >
      {(items) => <AlbumResults items={items} />}
    </ResultSection>
  );
}

function TrackSection({
  search,
  q,
  renderTrackActions,
  renderRowsScope,
  notice,
}: {
  search: SearchPageSearch;
  q: string;
  renderTrackActions: SearchResultsViewProps["renderTrackActions"];
  renderRowsScope: SearchResultsViewProps["renderTrackRowsScope"];
  notice: ReactNode;
}) {
  const tracks = useTrackResults(q, search.tracks_page ?? 1);
  return (
    <ResultSection
      title="曲目"
      unit="首"
      query={tracks}
      emptyText="没有找到匹配的曲目。"
      renderPageLink={(target, props) => <Link to="/search" search={{ ...search, tracks_page: target > 1 ? target : undefined }} {...props} />}
    >
      {(items) => (
        <>
          {notice}
          {renderRowsScope === undefined ? (
            <TrackResults items={items} renderTrackActions={renderTrackActions} />
          ) : (
            renderRowsScope(items, <TrackResults items={items} renderTrackActions={renderTrackActions} />)
          )}
        </>
      )}
    </ResultSection>
  );
}
