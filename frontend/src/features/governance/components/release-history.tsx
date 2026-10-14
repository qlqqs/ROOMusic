import { Link } from "@tanstack/react-router";
import { useRef } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Disclosure, QueryContent, Skeleton } from "../../../shared/ui";
import type { OperationItemDTO } from "../api/decoders";
import { RELEASE_HISTORY_LIMIT, useOperations } from "../hooks/use-operations";
import type { ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { useRevertFlow } from "../hooks/use-revert-flow";
import { OperationList } from "./operation-list";
import { ReleaseCommandNotice } from "./release-command-notice";
import { RevertOperationDialog } from "./revert-operation-dialog";
import styles from "./release-history.module.css";

/** 展开之后才挂载（Disclosure 折叠时不渲染内容），所以请求也在展开之后才发出：这个版本最近的几条记录，更早的在操作记录页。 */
function HistoryBody({ releaseId, onRevert }: { releaseId: string; onRevert: (item: OperationItemDTO) => void }) {
  const history = useOperations({ releaseId, page: 1, pageSize: RELEASE_HISTORY_LIMIT });
  return (
    <QueryContent query={history} loading={<Skeleton label="正在加载修改记录" lines={3} />}>
      {({ items, pagination }) =>
        pagination.total === 0 ? (
          <p>这个版本还没有修改记录。管理员修改发行信息或调整归组之后，记录会出现在这里。</p>
        ) : (
          <>
            <OperationList caption="修改记录" items={items} showRelease={false} onRevert={onRevert} />
            <p className={styles.more}>
              <span>{pagination.total > items.length ? `只显示最近 ${formatCount(items.length)} 条，共 ${formatCount(pagination.total)} 条。` : `共 ${formatCount(pagination.total)} 条。`}</span>
              <Link to="/operations" search={{ release: releaseId }}>
                全部记录
              </Link>
            </p>
          </>
        )
      }
    </QueryContent>
  );
}

type ReleaseHistoryProps = {
  release: ReleaseTarget;
  /** 撤销的命令：页面持有（撤销归组之后页面会换到另一个专辑组，详情组件重新挂载，结果提示仍要留在页面上）。 */
  commands: ReleaseCommands;
};

/**
 * 专辑详情（管理员）底部折叠的“修改记录”：这个版本最近 5 条记录，可撤销的有“撤销”，另有到“全部记录”的链接。
 * 展开之后才读取；撤销的结果提示放在折叠区块外面，收起或换到别的专辑组之后仍然看得到。换版本时（key）回到折叠。
 */
export function ReleaseHistory({ release, commands }: ReleaseHistoryProps) {
  const summaryRef = useRef<HTMLElement>(null);
  const revert = useRevertFlow(commands, summaryRef);
  return (
    <div className={styles.history}>
      <ReleaseCommandNotice state={commands.state} releaseId={release.id} hideFailure={revert.target !== null} onRetry={() => commands.retry()} onDismiss={commands.dismiss} />
      <Disclosure title="修改记录" summaryRef={summaryRef}>
        <HistoryBody releaseId={release.id} onRevert={revert.open} />
      </Disclosure>
      <RevertOperationDialog item={revert.target} commands={commands} onClose={revert.close} />
    </div>
  );
}
