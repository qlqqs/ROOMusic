import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import { adoptMusicBrainzName, fetchCurationConflicts, selectMatchCandidate } from "../api/endpoints";
import { curationKeys } from "../query-keys";

/** 有冲突的专辑一页的条数。 */
export const CONFLICT_PAGE_SIZE = 20;

/** 有冲突的专辑一页（整理页）。 */
export function useCurationConflicts(page: number) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: curationKeys.conflictPage(generation, page),
    queryFn: ({ signal }) => fetchCurationConflicts(page, CONFLICT_PAGE_SIZE, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/**
 * 挑一张候选：同一次挑选用同一个幂等键（失败后点同一张重试仍是同一个键），成功后有冲突的列表、这一张的结论与进度重新读取，
 * refresh 让专辑与目录跟着更新（由 app 提供）。
 */
export function useSelectMatchCandidate(releaseId: string, refresh: () => void) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const keys = useRef(new Map<string, string>());
  return useMutation({
    mutationFn: (choice: { source: string; snapshotId: string; externalId: string }) => {
      const id = `${choice.snapshotId}/${choice.externalId}`;
      const key = keys.current.get(id) ?? newIdempotencyKey();
      keys.current.set(id, key);
      return selectMatchCandidate(releaseId, choice, key);
    },
    onSuccess: () => {
      refresh();
      if (!scope.isCurrent()) return;
      void queryClient.invalidateQueries({ queryKey: curationKeys.conflicts(scope.generation) });
      void queryClient.invalidateQueries({ queryKey: curationKeys.matchResults(scope.generation, releaseId) });
      void queryClient.invalidateQueries({ queryKey: curationKeys.progress(scope.generation) });
    },
  });
}

/** 一键采用 MusicBrainz 的写法；成功后 refresh 重读专辑（由 app 提供）。 */
export function useAdoptMusicBrainzName(releaseId: string, refresh: () => void) {
  return useMutation({
    mutationFn: (name: { targetId: string; field: string; basisOperationId: string; revision: number }) => adoptMusicBrainzName(releaseId, name, newIdempotencyKey()),
    onSuccess: () => refresh(),
  });
}
