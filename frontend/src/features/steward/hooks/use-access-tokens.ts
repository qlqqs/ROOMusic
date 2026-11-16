import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useViewerScope } from "../../auth";
import type { TokenDTO } from "../api/decoders";
import { createToken, fetchTokens, revokeToken, type NewToken } from "../api/endpoints";
import { stewardKeys } from "../query-keys";

export function tokensQueryOptions(generation: number) {
  return queryOptions({
    queryKey: stewardKeys.tokens(generation),
    queryFn: ({ signal }) => fetchTokens(signal),
  });
}

/** 自己的访问令牌（未吊销的）与当前能不能用。 */
export function useAccessTokens() {
  const { generation } = useViewerScope();
  return useQuery({ ...tokensQueryOptions(generation), select: (response) => response.data });
}

/** 签发：成功后明文在返回值里，只留在调用方的组件状态中，不进查询缓存。无论成败都刷新列表。 */
export function useCreateToken() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async (input: NewToken) => (await createToken(input)).data,
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.tokens(generation) }),
  });
}

/** 吊销：再吊销一次已吊销的令牌只会得到 404，结果不确定时刷新列表即可核对。 */
export function useRevokeToken() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useMutation({
    mutationFn: async (token: Pick<TokenDTO, "id" | "name">) => {
      await revokeToken(token.id);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: stewardKeys.tokens(generation) }),
  });
}
