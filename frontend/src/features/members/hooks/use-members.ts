import { queryOptions, useQuery } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import { fetchMembers } from "../api/endpoints";
import { memberKeys } from "../query-keys";

export function membersQueryOptions(generation: number) {
  return queryOptions({
    queryKey: memberKeys.list(generation),
    queryFn: ({ signal }) => fetchMembers(signal),
  });
}

/** 管理员的成员列表；后端不分页。非管理员不应调用（后端会拒绝）。 */
export function useMembers() {
  const { generation } = useViewerScope();
  return useQuery({ ...membersQueryOptions(generation), select: (response) => response.data.items });
}

export type MemberSummary = { enabled: number; disabled: number };

export function summarizeMembers(members: readonly MemberDTO[]): MemberSummary {
  const disabled = members.filter((member) => member.disabled).length;
  return { enabled: members.length - disabled, disabled };
}
