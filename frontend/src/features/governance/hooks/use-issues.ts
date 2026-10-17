import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import { fetchIssues, fetchReleaseIssues } from "../api/endpoints";
import type { IssueCategory } from "../issues-search";
import { governanceKeys } from "../query-keys";

/** 问题列表每页的条数（后端默认 50，上限 100）；显式传给后端，不依赖它的默认值。 */
export const ISSUE_PAGE_SIZE = 30;

type IssuesOptions = { status: "open" | "reviewed"; category: IssueCategory | undefined; page: number };

/**
 * 问题列表的一页（仅管理员；非管理员不应调用，后端会拒绝）。翻页与换筛选时保留上一页的数据直到新一页到达，列表不会闪空。
 * 问题会随扫描变化：每次打开都重新读取（staleTime 为 0，先显示缓存、随即刷新）；核对与修改之后由命令让 governance 的全部查询失效。
 */
export function useIssues({ status, category, page }: IssuesOptions) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.issuePage(generation, { status, category: category ?? null, page, pageSize: ISSUE_PAGE_SIZE }),
    queryFn: ({ signal }) => fetchIssues({ status, category, page, pageSize: ISSUE_PAGE_SIZE, signal }),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: 0,
  });
}

/**
 * 一个版本的问题（仅管理员）。依据令牌要与服务器当前的情况一致：同样每次打开都重新读取，核对之前看到的就是最新的；
 * 换版本时不保留上一个版本的数据（那是另一个版本的问题）。
 */
export function useReleaseIssues(releaseId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.releaseIssues(generation, releaseId),
    queryFn: ({ signal }) => fetchReleaseIssues(releaseId, signal),
    select: (response) => response.data,
    staleTime: 0,
  });
}
