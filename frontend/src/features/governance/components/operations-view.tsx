import { Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Pagination, QueryContent, SettingsGroup, Skeleton } from "../../../shared/ui";
import type { OperationPageDTO } from "../api/decoders";
import { sourceName } from "../change-summary";
import { OPERATION_PAGE_SIZE, useOperations, type CurationFilter } from "../hooks/use-operations";
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
  /** 看这位艺人的记录（URL 的 artist，M5 ⑤）；与上面两项同时给出时取并集；undefined 是不按艺人筛选。 */
  artistId?: string | undefined;
  /** 自动整理的筛选（URL 的 source、provider、batch）：与目标筛选之间是“并且”。 */
  curation?: CurationFilter | undefined;
  /** 撤销的命令：由 app 创建（刷新什么、要不要跟随导航由 app 决定）。 */
  commands: ReleaseCommands;
};

/**
 * 筛选时说明在看谁的记录：只按版本筛选时标题取自已加载记录里这个版本现在的标题，没有记录时只说“这个版本”；
 * 按专辑筛选（专辑详情的“全部记录”同时带版本与专辑）说明是这个版本和这张专辑的记录。
 */
function FilterNote({
  releaseId,
  groupId,
  artistId,
  curation,
  data,
}: {
  releaseId: string | undefined;
  groupId: string | undefined;
  artistId: string | undefined;
  curation: CurationFilter | undefined;
  data: OperationPageDTO | undefined;
}) {
  const target = releaseId !== undefined || groupId !== undefined || artistId !== undefined;
  const title = data?.items.find((item) => item.release !== null)?.release?.title;
  const artistName = data?.items.find((item) => item.artist?.id === artistId)?.artist?.name;
  const subject =
    artistId !== undefined && releaseId === undefined && groupId === undefined
      ? artistName === undefined
        ? "这位艺人"
        : `“${artistName}”`
      : groupId === undefined
        ? title === undefined
          ? "这个版本"
          : `《${title}》`
        : releaseId === undefined
          ? "这张专辑"
          : "这个版本和它所在的专辑";
  const automatic = curationText(curation);
  const text = target && automatic !== null ? `只显示${subject}${automatic}的记录。` : target ? `只显示${subject}的记录。` : `只显示${automatic ?? ""}的记录。`;
  return (
    <p className={styles.filter}>
      <span>{text}</span>
      <Link to="/settings/operations" search={{}}>
        查看全部记录
      </Link>
    </p>
  );
}

/** 自动整理筛选的说明：这一批、来自某个源，或全部自动整理；没有筛选时为 null。 */
function curationText(curation: CurationFilter | undefined): string | null {
  if (curation === undefined) return null;
  const parts = [
    curation.batchId === undefined ? null : "这一批",
    curation.provider === undefined ? null : `来自 ${sourceName(curation.provider)} 的`,
  ].filter((part) => part !== null);
  if (parts.length > 0) return `${parts.join("、")}自动整理`;
  return curation.source === "curation" ? "自动整理" : null;
}

/**
 * 操作记录页的内容（管理员）：管理员对专辑的修改、归组调整、默认版本、对艺人的合并、拆开与改名，以及撤销，最新的在前，可以按版本、专辑与艺人筛选，可撤销的有“撤销”。
 * 页码来自 URL：页码超出末页时用替换历史的方式改到末页（没有数据时回到第 1 页）。
 * 撤销之后记录与“能不能撤销”都来自服务器的最新状态：命令让 governance 的查询失效，列表随之刷新。
 */
export function OperationsView({ page, releaseId, groupId, artistId, curation, commands }: OperationsViewProps) {
  const operations = useOperations({ releaseId, groupId, artistId, curation, page, pageSize: OPERATION_PAGE_SIZE });
  const automatic = curationText(curation) !== null;
  const targeted = releaseId !== undefined || groupId !== undefined || artistId !== undefined;
  const filtered = targeted || automatic;
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const revert = useRevertFlow(commands, headingRef);

  const pagination = operations.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !operations.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/settings/operations", search: (previous) => ({ ...previous, page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  // 有记录时总条数与分页放在卡片下方。
  const listed = operations.data !== undefined && !outOfRange && operations.data.pagination.total > 0 ? operations.data : null;
  return (
    <>
      {filtered ? <FilterNote releaseId={releaseId} groupId={groupId} artistId={artistId} curation={curation} data={operations.data} /> : null}
      <ReleaseCommandNotice state={commands.state} releaseId={null} hideFailure={revert.target !== null} onRetry={() => commands.retry()} onDismiss={commands.dismiss} />
      <SettingsGroup
        title={!filtered ? "全部记录" : !targeted ? "自动整理的记录" : artistId !== undefined && releaseId === undefined && groupId === undefined ? "这位艺人的记录" : groupId === undefined ? "这个版本的记录" : releaseId === undefined ? "这张专辑的记录" : "这个版本和专辑的记录"}
        headingRef={headingRef}
        footer={
          listed ? (
            <div aria-busy={operations.isPlaceholderData} className={styles.footer}>
              <p className={styles.note}>
                共 {formatCount(listed.pagination.total)} 条记录
              </p>
              <Pagination
                label="操作记录分页"
                page={listed.pagination.page}
                pageCount={pageCountOf(listed.pagination.total, listed.pagination.page_size)}
                renderLink={(target, props) => (
                  <Link to="/settings/operations" search={(previous) => ({ ...previous, page: target > 1 ? target : undefined })} {...props} />
                )}
              />
            </div>
          ) : null
        }
      >
        <QueryContent query={operations} loading={<Skeleton label="正在加载操作记录" lines={4} />}>
          {(data) =>
            outOfRange ? (
              <Skeleton label="正在加载操作记录" lines={4} />
            ) : data.pagination.total === 0 ? (
              <p>{!filtered ? "还没有操作记录。" : !targeted ? "还没有这些自动整理的记录。" : groupId === undefined ? "这个版本还没有操作记录。" : "还没有这些记录。"}</p>
            ) : (
              <div aria-busy={operations.isPlaceholderData}>
                <OperationList caption="操作记录" items={data.items} showRelease onRevert={revert.open} />
              </div>
            )
          }
        </QueryContent>
      </SettingsGroup>
      <RevertOperationDialog item={revert.target} commands={commands} onClose={revert.close} />
    </>
  );
}
