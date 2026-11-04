import type { QueryClient } from "@tanstack/react-query";
import { newIdempotencyKey } from "../../shared/lib/ids";
import { postPlay } from "./api/endpoints";
import { createPlayReporter, type PlayReporter } from "./play-reporter";
import { listeningKeys } from "./query-keys";

/** 某个会话代次的上报器：上报成功后只让这个查看者的播放历史查询（“最近播放”、历史页与“播放全部”的候选）失效。播放器与引擎一起创建它，会话结束时作废。 */
export function createViewerPlayReporter(queryClient: QueryClient, generation: number): PlayReporter {
  return createPlayReporter({
    send: (report, idempotencyKey, signal) => postPlay(report, idempotencyKey, signal),
    onRecorded: () => void queryClient.invalidateQueries({ queryKey: listeningKeys.all(generation) }),
    newKey: newIdempotencyKey,
  });
}
