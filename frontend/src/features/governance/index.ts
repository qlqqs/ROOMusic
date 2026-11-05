// governance 的公开入口：管理员对发行的受治理写操作（编辑发行信息、调整归组、核对问题、撤销）与对艺人的合并、拆开、改名（M5 ⑤）的界面与命令 hook，
// 元数据问题页、待认领页、操作记录页与专辑详情里的“修改记录”，以及它们的接口合同。其他 feature 与 app 只经这里使用，不引用 governance 内部文件。
export { ClaimsView } from "./components/claims-view";
export { parseClaimsSearch, type ClaimsSearch } from "./claims-search";
export { IssuesView } from "./components/issues-view";
export { parseIssuesSearch, type IssuesSearch } from "./issues-search";
export { OperationsView } from "./components/operations-view";
export { parseOperationsSearch, type OperationsSearch } from "./operations-search";
export { ReleaseAdminActions } from "./components/release-admin-actions";
export { ReleaseHistory } from "./components/release-history";
export { useReleaseCommands, type ReleaseCommands } from "./hooks/use-release-commands";
export { ArtistAdminActions } from "./components/artist-admin-actions";
export { ArtistCandidatesView, type ArtistCandidateStatus } from "./components/artist-candidates-view";
export { useArtistCommands, type ArtistCommands } from "./hooks/use-artist-commands";
export { governanceKeys } from "./query-keys";
