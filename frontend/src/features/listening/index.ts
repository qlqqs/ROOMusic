// listening 的公开入口：概览的“最近播放”区块、历史页的内容（播放动作经插槽由 app 组合）、历史条目的类型与显示规则与查询键。
// 计一次播放由服务端判定（播放区），浏览器只在收到 play_recorded 时让历史查询失效。外部组件不导入 feature 内部文件。
export { RecentPlaysPanel } from "./components/recent-plays-panel";
export { HistoryView } from "./components/history-view";
export { useHistoryQueue, type HistoryQueueCallbacks } from "./hooks/use-history-queue";
export { HISTORY_QUEUE_LIMIT, type HistoryItemDTO, type HistoryQueueDTO } from "./api/decoders";
export { historyArtistOf, historyTitleOf } from "./format";
export { listeningKeys } from "./query-keys";
