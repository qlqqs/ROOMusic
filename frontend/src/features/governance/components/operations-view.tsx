import { Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Pagination, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import type { OperationPageDTO } from "../api/decoders";
import { OPERATION_PAGE_SIZE, useOperations } from "../hooks/use-operations";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import { useRevertFlow } from "../hooks/use-revert-flow";
import { OperationList } from "./operation-list";
import { ReleaseCommandNotice } from "./release-command-notice";
import { RevertOperationDialog } from "./revert-operation-dialog";
import styles from "./operations-view.module.css";

type OperationsViewProps = {
  /** URL 里的页码，从 1 起。 */
  page: number;
  /** 只看这个版本的记录（URL 的 release）；undefined 是不按版本筛选。 */
  releaseId: string | undefined;
  /** 看这张专辑的记录（URL 的 group，默认版本的设定等专辑级记录）；与 releaseId 同时给出时取并集；undefined 是不按专辑筛选。 */
  groupId: string | undefined;
  /** 撤销的命令：由 app 创建（刷新什么、要不要跟随导航由 app 决定）。 */
  commands: ReleaseCommands;
};

/**
 * 筛选时说明在看谁的记录：只按版本筛选时标题取自已加载记录里这个版本现在的标题，没有记录时只说“这个版本”；
 * 按专辑筛选（专辑详情的“全部记录”同时带版本与专辑）说明是这个版本和这张专辑的记录。
 */
function FilterNote({ releaseId, groupId, data }: { releaseId: string | undefined; groupId: string | undefined; data: OperationPageDTO | undefined }) {
  const title = data?.items.find((item) => item.release !== null)?.release?.title;
  const subject =
    groupId === undefined ? (title === undefined ? "这个版本" : `《${title}》`) : releaseId === undefined ? "这张专辑" : "这个版本和它所在的专辑";
  return (
    <p className={styles.filter}>
      <span>只显示{subject}的记录。</span>
      <Link to="/operations" search={{}}>
        查看全部记录
      </Link>
    </p>
  );
}

/**
 * 操作记录页的内容（管理员）：管理员对专辑的修改、归组调整、默认版本与撤销，最新的在前，可以按版本与专辑筛选，可撤销的有“撤销”。
 * 页码来自 URL：页码超出末页时用替换历史的方式改到末页（没有数据时回到第 1 页）。
 * 撤销之后记录与“能不能撤销”都来自服务器的最新状态：命令让 governance 的查询失效，列表随之刷新。
 */
export function OperationsView({ page, releaseId, groupId, commands }: OperationsViewProps) {
  const operations = useOperations({ releaseId, groupId, page, pageSize: OPERATION_PAGE_SIZE });
  const filtered = releaseId !== undefined || groupId !== undefined;
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const revert = useRevertFlow(commands, headingRef);

  const pagination = operations.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !operations.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/operations", search: (previous) => ({ ...previous, page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  return (
    <>
      {filtered ? <FilterNote releaseId={releaseId} groupId={groupId} data={operations.data} /> : null}
      <ReleaseCommandNotice state={commands.state} releaseId={null} hideFailure={revert.target !== null} onRetry={() => commands.retry()} onDismiss={commands.dismiss} />
      <Panel title={!filtered ? "全部记录" : groupId === undefined ? "这个版本的记录" : releaseId === undefined ? "这张专辑的记录" : "这个版本和专辑的记录"} headingRef={headingRef}>
        <QueryContent query={operations} loading={<Skeleton label="正在加载操作记录" lines={4} />}>
          {(data) =>
            outOfRange ? (
              <Skeleton label="正在加载操作记录" lines={4} />
            ) : data.pagination.total === 0 ? (
              <p>{!filtered ? "还没有操作记录。" : groupId === undefined ? "这个版本还没有操作记录。" : "还没有这些记录。"}</p>
            ) : (
              <div aria-busy={operations.isPlaceholderData}>
                <OperationList caption="操作记录" items={data.items} showRelease onRevert={revert.open} />
                <p className={styles.note}>
                  共 {formatCount(data.pagination.total)} 条记录
                </p>
                <Pagination
                  label="操作记录分页"
                  page={data.pagination.page}
                  pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                  renderLink={(target, props) => (
                    <Link to="/operations" search={(previous) => ({ ...previous, page: target > 1 ? target : undefined })} {...props} />
                  )}
                />
              </div>
            )
          }
        </QueryContent>
      </Panel>
      <RevertOperationDialog item={revert.target} commands={commands} onClose={revert.close} />
    </>
  );
}
