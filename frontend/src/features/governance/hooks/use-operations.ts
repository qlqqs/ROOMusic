import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchOperations } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/** 操作记录页每页的条数（后端默认 50，上限 100）；显式传给后端，不依赖它的默认值。 */
export const OPERATION_PAGE_SIZE = 20;
/** 专辑详情里“修改记录”只显示这个版本最近的几条。 */
export const RELEASE_HISTORY_LIMIT = 5;

/** 操作记录的自动整理筛选：只看自动整理、来自某个源、某一批。 */
export type CurationFilter = { source?: "curation" | undefined; provider?: string | undefined; batchId?: string | undefined };

type OperationsOptions = {
  /** 只看这个发行的记录；undefined 是全部。 */
  releaseId?: string | undefined;
  /** 只看这张专辑的记录（默认版本的设定等）；与 releaseId 同时给出时取并集。 */
  groupId?: string | undefined;
  /** 只看这位艺人的记录（M5 ⑤）。 */
  artistId?: string | undefined;
  /** 自动整理的筛选（与目标筛选之间是“并且”）。 */
  curation?: CurationFilter | undefined;
  page: number;
  pageSize: number;
};

/**
 * 操作记录的一页（仅管理员；非管理员不应调用，后端会拒绝）。翻页时保留上一页的数据直到新一页到达，表格不会闪空。
 * 记录与“能不能撤销”要反映服务器当前的状态：自己的修改与撤销之后由命令让 governance 的全部查询失效，
 * 别的管理员的修改靠每次打开都重新读取（staleTime 为 0，先显示缓存、随即刷新），不沿用全局的 30 秒新鲜期。
 */
export function useOperations({ releaseId, groupId, artistId, curation, page, pageSize }: OperationsOptions) {
  const { generation } = useViewerScope();
  const source = curation?.source;
  const provider = curation?.provider;
  const batchId = curation?.batchId;
  return useQuery({
    queryKey: governanceKeys.operationPage(generation, {
      releaseId: releaseId ?? null,
      groupId: groupId ?? null,
      artistId: artistId ?? null,
      source: source ?? null,
      provider: provider ?? null,
      batchId: batchId ?? null,
      page,
      pageSize,
    }),
    queryFn: ({ signal }) => fetchOperations({ page, pageSize, releaseId, groupId, artistId, source, provider, batchId, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}
