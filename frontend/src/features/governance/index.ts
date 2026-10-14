// governance 的公开入口：管理员对发行的受治理写操作（编辑发行信息、调整归组、撤销）的界面与命令 hook，
// 操作记录页与专辑详情里的“修改记录”，以及它们的接口合同。其他 feature 与 app 只经这里使用，不引用 governance 内部文件。
export { OperationsView } from "./components/operations-view";
export { parseOperationsSearch, type OperationsSearch } from "./operations-search";
export { ReleaseAdminActions } from "./components/release-admin-actions";
export { ReleaseHistory } from "./components/release-history";
export { useReleaseCommands, type ReleaseCommands } from "./hooks/use-release-commands";
