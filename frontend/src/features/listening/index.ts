// listening 的公开入口：概览的“最近播放”区块、历史页的内容（播放动作经插槽由 app 组合）、历史条目的类型与显示规则，
// 以及播放上报器（引擎交出一次播放后，经它带幂等键上报并有限重试）与查询键。外部组件不导入 feature 内部文件。
export { RecentPlaysPanel } from "./components/recent-plays-panel";
export { HistoryView } from "./components/history-view";
export { useHistoryQueue, type HistoryQueueCallbacks } from "./hooks/use-history-queue";
export { HISTORY_QUEUE_LIMIT, type HistoryItemDTO, type HistoryQueueDTO } from "./api/decoders";
export { historyArtistOf, historyTitleOf } from "./format";
export { createPlayReporter, MAX_PENDING_REPORTS, RETRY_DELAYS_MS, type PlayReporter, type PlayReporterDeps } from "./play-reporter";
export type { PlayReport } from "./api/endpoints";
export { createViewerPlayReporter } from "./viewer-play-reporter";
export { listeningKeys } from "./query-keys";
