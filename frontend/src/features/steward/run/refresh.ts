import type { QueryClient } from "@tanstack/react-query";
import { markConversationRead } from "../api/endpoints";
import { stewardKeys } from "../query-keys";
import { isViewing } from "./viewing";

/** 运行结束之后重读这个会话与列表（以落库内容为准）。返回的 Promise 在两者都重读完（或标为过期）后完成。 */
export function refreshConversation(queryClient: QueryClient, generation: number, id: string): Promise<unknown> {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: stewardKeys.conversation(generation, id) }),
    queryClient.invalidateQueries({ queryKey: stewardKeys.conversations(generation) }),
  ]);
}

/**
 * 一次运行结束（C10）：用户还停在这个会话里时先标记已读，再重读会话、列表与设置（入口提示点的未读数）。
 * 标记失败不影响重读：未读会在下次打开时清掉。
 */
export async function refreshAfterRun(queryClient: QueryClient, generation: number, id: string): Promise<unknown> {
  if (isViewing(id)) await markConversationRead(id).catch(() => undefined);
  return Promise.all([refreshConversation(queryClient, generation, id), queryClient.invalidateQueries({ queryKey: stewardKeys.settings(generation) })]);
}
