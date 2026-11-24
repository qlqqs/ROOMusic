import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Button, FailureNotice, Page } from "../../../shared/ui";
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
 * 搜索结果页：艺人（就绪时）、专辑、曲目各一栏，各自加载、失败、重试与分页（只改自己那一栏的页码）；几栏都失败时合成一条错误。
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
        <ResultSections
          search={search}
          q={q}
          artistsReady={artistsReady}
          renderTrackActions={renderTrackActions}
          renderTrackRowsScope={renderTrackRowsScope}
          tracksNotice={tracksNotice}
        />
      )}
    </Page>
  );
}

type Results = {
  artists: ReturnType<typeof useArtistResults>;
  albums: ReturnType<typeof useAlbumResults>;
  tracks: ReturnType<typeof useTrackResults>;
};

/** 这一栏已经读到第 1 页而且一条结果也没有（不是翻到了超出范围的页码）。 */
function settledEmpty(query: { data?: { items: readonly unknown[]; pagination: { total: number; page: number } } | undefined }): boolean {
  const data = query.data;
  return data !== undefined && data.pagination.total === 0 && data.items.length === 0 && data.pagination.page === 1;
}

/**
 * 各栏的查询在这里发出：出现的几栏都失败且没有旧结果时（通常是同一个原因，例如服务器没有响应），只显示一条错误和一个“重试”（重试所有栏），
 * 不在每一栏重复同一句话；只有部分栏失败时，各栏照常各自显示错误与重试。
 * 几栏都读到了、全都没有结果时只说一句；有的栏有结果时，没有结果的栏不出现（不留一块只写“没有找到”的区块）。
 */
function ResultSections({
  search,
  q,
  artistsReady,
  renderTrackActions,
  renderTrackRowsScope,
  tracksNotice,
}: Omit<SearchResultsViewProps, "search"> & { search: SearchPageSearch; q: string; artistsReady: boolean }) {
  const results: Results = {
    artists: useArtistResults(q, search.artists_page ?? 1, artistsReady),
    albums: useAlbumResults(q, search.albums_page ?? 1),
    tracks: useTrackResults(q, search.tracks_page ?? 1),
  };
  const shown = artistsReady ? [results.artists, results.albums, results.tracks] : [results.albums, results.tracks];
  if (shown.every((query) => query.status === "error" && query.data === undefined)) {
    return (
      <FailureNotice
        error={results.albums.error}
        title="暂时搜不到结果"
        action={
          shown.some((query) => query.isFetching) ? (
            <Button busy>正在重试…</Button>
          ) : (
            <Button onClick={() => shown.forEach((query) => void query.refetch())}>重试</Button>
          )
        }
      />
    );
  }
  if (shown.every(settledEmpty)) {
    return <p className={styles.empty}>没有找到与“{q}”有关的{artistsReady ? "艺人、" : ""}专辑或曲目。</p>;
  }
  const visible = (query: Parameters<typeof settledEmpty>[0]) => !settledEmpty(query);
  return (
    <div className={styles.sections}>
      {artistsReady && visible(results.artists) ? <ArtistSection search={search} artists={results.artists} /> : null}
      {visible(results.albums) ? <AlbumSection search={search} albums={results.albums} /> : null}
      {visible(results.tracks) ? (
        <TrackSection search={search} tracks={results.tracks} renderTrackActions={renderTrackActions} renderRowsScope={renderTrackRowsScope} notice={tracksNotice} />
      ) : null}
    </div>
  );
}

function ArtistSection({ search, artists }: { search: SearchPageSearch; artists: Results["artists"] }) {
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

function AlbumSection({ search, albums }: { search: SearchPageSearch; albums: Results["albums"] }) {
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
  tracks,
  renderTrackActions,
  renderRowsScope,
  notice,
}: {
  search: SearchPageSearch;
  tracks: Results["tracks"];
  renderTrackActions: SearchResultsViewProps["renderTrackActions"];
  renderRowsScope: SearchResultsViewProps["renderTrackRowsScope"];
  notice: ReactNode;
}) {
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
