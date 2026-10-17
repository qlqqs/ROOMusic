import { IssuesView, useReleaseCommands, type IssuesSearch } from "../../features/governance";
import { Page } from "../../shared/ui";
import { useReleaseDataRefresh } from "./use-release-refresh";

/** 问题页不跟随归组的变化：核对与撤销都不移动版本，编辑发行信息也不移动。 */
function stayOnPage() {}

/**
 * 元数据问题：扫描拿不准的字段、同一目录的归组冲突与缺失的曲目，按版本列出，逐条说明原因与依据，可以标记为已核对、修改与撤销。
 * 管理员专用，由路由层的 AdminGate 把关。状态、类别、页码与选中的版本都来自地址。核对与修改之后，专辑列表、版本详情与概览的数字一起刷新。
 */
export function IssuesPage({ search }: { search: IssuesSearch }) {
  const refresh = useReleaseDataRefresh();
  const commands = useReleaseCommands({ refresh, onMoved: stayOnPage });
  return (
    <Page title="元数据问题" description="扫描拿不准、需要人看一眼的地方，按版本列出。字段可以在“编辑发行信息”里修改；来源与介质类型、归组冲突与缺失曲目看过之后可以标记为已核对，扫描发现情况变了会重新提醒。">
      <IssuesView search={search} commands={commands} />
    </Page>
  );
}
