import { IssuesView, type IssuesSearch } from "../../features/governance";
import { Page } from "../../shared/ui";

/**
 * 元数据问题：扫描拿不准的字段、同一目录的归组冲突与缺失的曲目，按版本列出，逐条说明原因与依据。管理员专用，由路由层的 AdminGate 把关。
 * 状态、类别、页码与选中的版本都来自地址。
 */
export function IssuesPage({ search }: { search: IssuesSearch }) {
  return (
    <Page title="元数据问题" description="扫描拿不准、需要人看一眼的地方，按版本列出。字段可以在“编辑发行信息”里修改；来源与介质类型、归组冲突与缺失曲目看过之后可以标记为已核对，扫描发现情况变了会重新提醒。">
      <IssuesView search={search} />
    </Page>
  );
}
