import type { QueryClient } from "@tanstack/react-query";
import { stewardKeys } from "../query-keys";

/** 运行结束之后重读这个会话与列表（以落库内容为准）。返回的 Promise 在两者都重读完（或标为过期）后完成。 */
export function refreshConversation(queryClient: QueryClient, generation: number, id: string): Promise<unknown> {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: stewardKeys.conversation(generation, id) }),
    queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) }),
  ]);
}
