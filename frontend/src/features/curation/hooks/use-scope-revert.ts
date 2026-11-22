import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import { governanceKeys } from "../../governance";
import { fetchCurationBatch, revertCurationScope, type RevertScope } from "../api/endpoints";
import { curationKeys } from "../query-keys";

export type ScopeRevertState =
  | { phase: "idle" }
  | { phase: "running"; reverted: number }
  | {
      phase: "done";
      /** 这次撤销实际撤掉的条数；中途重试过时是下限（重试之前已撤掉、响应丢失的那段不计入）。 */
      reverted: number;
      atLeast: boolean;
      /** 按 operation_id 去重后的跳过项。 */
      skipped: { operationId: string; code: string }[];
      /** 按批撤销完成后读到的权威总数。 */
      batch: { operations: number; reverted: number } | null;
    }
  | { phase: "interrupted"; reverted: number; error: unknown };

type Progress = { scope: RevertScope; key: string; cursor: string | null; reverted: number; skipped: Map<string, string>; retried: boolean };

/**
 * 按批或按来源撤销：一次撤销的各段用同一个幂等键，带上一段的游标，直到游标为空；游标没有前进时停止（防止服务器异常时无限循环）。
 * 网络失败或结果不确定时停在 interrupted，`resume` 用同一个键与当前游标继续（同键重试不会重复撤销）。
 * 完成后让自动整理与操作记录的查询失效，再调用 app 传入的 refresh（目录、歌词等也可能变了）。
 */
export function useScopeRevert(refresh: () => void) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const [state, setState] = useState<ScopeRevertState>({ phase: "idle" });
  const progress = useRef<Progress | null>(null);

  const run = useCallback(async () => {
    const current = progress.current;
    if (current === null) return;
    setState({ phase: "running", reverted: current.reverted });
    try {
      for (;;) {
        const response = await revertCurationScope(current.scope, current.cursor, current.key);
        current.reverted += response.data.reverted;
        for (const item of response.data.skipped) current.skipped.set(item.operation_id, item.code);
        const next = response.data.next_cursor;
        if (next === null || next === current.cursor) break;
        current.cursor = next;
        if (!scope.isCurrent()) return;
        setState({ phase: "running", reverted: current.reverted });
      }
      let batch: { operations: number; reverted: number } | null = null;
      if (current.scope.kind === "batch") {
        try {
          const read = await fetchCurationBatch(current.scope.id);
          batch = { operations: read.data.operations, reverted: read.data.reverted };
        } catch {
          batch = null;
        }
      }
      if (!scope.isCurrent()) return;
      setState({
        phase: "done",
        reverted: current.reverted,
        atLeast: current.retried,
        skipped: [...current.skipped].map(([operationId, code]) => ({ operationId, code })),
        batch,
      });
    } catch (error) {
      if (!scope.isCurrent()) return;
      current.retried = true;
      setState({ phase: "interrupted", reverted: current.reverted, error });
    } finally {
      if (scope.isCurrent()) {
        void queryClient.invalidateQueries({ queryKey: curationKeys.all(scope.generation) });
        void queryClient.invalidateQueries({ queryKey: governanceKeys.all(scope.generation) });
        refresh();
      }
    }
  }, [queryClient, refresh, scope]);

  const start = useCallback(
    (target: RevertScope) => {
      progress.current = { scope: target, key: newIdempotencyKey(), cursor: null, reverted: 0, skipped: new Map(), retried: false };
      void run();
    },
    [run],
  );
  const resume = useCallback(() => void run(), [run]);
  const reset = useCallback(() => {
    progress.current = null;
    setState({ phase: "idle" });
  }, []);
  return { state, start, resume, reset };
}
