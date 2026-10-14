import { OperationsView, useReleaseCommands } from "../../features/governance";
import { Page } from "../../shared/ui";
import { useReleaseDataRefresh } from "./use-release-refresh";

/** 操作记录页不跟随归组的变化：撤销之后留在记录页，专辑的新位置由列表里的链接给出。 */
function stayOnPage() {}

/**
 * 操作记录：管理员对专辑的修改、归组调整与撤销。管理员专用，由路由层的 AdminGate 把关。
 * page 与 release 来自地址（页码、只看某个版本）。目录的操作记录仍在“扫描目录”页，不在这里。
 */
export function OperationsPage({ page, releaseId }: { page: number; releaseId: string | undefined }) {
  const refresh = useReleaseDataRefresh();
  const commands = useReleaseCommands({ refresh, onMoved: stayOnPage });
  return (
    <Page title="操作记录" description="管理员对专辑信息的修改、归组调整与撤销，最新的在前。只有最近一次修改可以撤销；目录的新增、停用与恢复记录在“扫描目录”页。">
      <OperationsView page={page} releaseId={releaseId} commands={commands} />
    </Page>
  );
}
