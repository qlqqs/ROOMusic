// listening 的公开入口：播放上报器（引擎交出一次播放后，经它带幂等键上报并有限重试）与查询键。
// 概览的“最近播放”区块在界面步骤加入这里。外部组件不导入 feature 内部文件。
export { createPlayReporter, MAX_PENDING_REPORTS, RETRY_DELAYS_MS, type PlayReporter, type PlayReporterDeps } from "./play-reporter";
export type { PlayReport } from "./api/endpoints";
export { createViewerPlayReporter } from "./viewer-play-reporter";
export { listeningKeys } from "./query-keys";
