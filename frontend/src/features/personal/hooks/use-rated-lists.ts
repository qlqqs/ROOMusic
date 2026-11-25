import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { RatedAlbumsPageDTO, RatedArtistsPageDTO, RatedTracksPageDTO } from "../api/decoders";
import { fetchRatedAlbums, fetchRatedArtists, fetchRatedTracks, type RatingFilter } from "../api/ratings";
import { personalKeys } from "../query-keys";

/** 评分列表每页的条目数（同收藏）。 */
export const RATINGS_PAGE_SIZE = 48;

/** 评过分的曲目的一页。键在评分的范围下：任何一处评分写入后失效，这一页按新值重新读取。 */
export function useRatedTracks(filter: RatingFilter, page: number) {
  const { generation } = useViewerScope();
  const params = { ...filter, page, pageSize: RATINGS_PAGE_SIZE };
  return useQuery({
    queryKey: personalKeys.ratedList(generation, "track", params),
    queryFn: ({ signal }) => fetchRatedTracks({ ...params, signal }),
    select: (response): RatedTracksPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}

export function useRatedAlbums(filter: RatingFilter, page: number) {
  const { generation } = useViewerScope();
  const params = { ...filter, page, pageSize: RATINGS_PAGE_SIZE };
  return useQuery({
    queryKey: personalKeys.ratedList(generation, "album", params),
    queryFn: ({ signal }) => fetchRatedAlbums({ ...params, signal }),
    select: (response): RatedAlbumsPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}

export function useRatedArtists(filter: RatingFilter, page: number) {
  const { generation } = useViewerScope();
  const params = { ...filter, page, pageSize: RATINGS_PAGE_SIZE };
  return useQuery({
    queryKey: personalKeys.ratedList(generation, "artist", params),
    queryFn: ({ signal }) => fetchRatedArtists({ ...params, signal }),
    select: (response): RatedArtistsPageDTO => response.data,
    placeholderData: keepPreviousData,
  });
}
