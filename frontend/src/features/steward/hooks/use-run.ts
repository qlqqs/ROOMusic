import { useQueryClient } from "@tanstack/react-query";
import { isHttpFailure } from "../../../shared/api";
import { useCallback, useSyncExternalStore } from "react";
import { useViewerScope } from "../../auth";
import { stewardKeys } from "../query-keys";
import type { ConversationDetailDTO } from "../api/decoders";
import { isIncomplete, turnsFromMessages, type Turn } from "../run/reduce";
import { getRunSnapshot, startRun, subscribeRuns, type ActiveRun, type RunEnd, type RunSnapshot, type StartResult } from "../run/run-store";
import { refreshConversation } from "../run/refresh";
import { isStewardDisabled } from "./use-steward-settings";

export function useRunSnapshot(): RunSnapshot {
  return useSyncExternalStore(subscribeRuns, getRunSnapshot);
}

/** 本页发起、还没结束的运行（按会话 ID）。 */
export function useRuns(): RunSnapshot["runs"] {
  return useRunSnapshot().runs;
}

/** 请求被拒绝是因为管理员关闭了成员使用管家：设置重读后入口随之隐藏、整页显示说明。 */
export function useStewardDisabledCheck() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  return useCallback(
    (error: unknown, disabled = isStewardDisabled(error)) => {
      if (disabled) void queryClient.invalidateQueries({ queryKey: stewardKeys.settings(generation) });
    },
    [queryClient, generation],
  );
}

/** 发消息（text）或从现有历史继续（null）。运行结束后重读会话与列表。 */
export function useStartRun() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const checkDisabled = useStewardDisabledCheck();
  return useCallback(
    async (conversationId: string, text: string | null, baseSequence: number): Promise<StartResult> => {
      const result = await startRun({ conversationId, text, baseSequence, refresh: () => refreshConversation(queryClient, generation, conversationId) });
      if (!result.started && isHttpFailure(result.failure, 403, "steward_disabled")) checkDisabled(null, true);
      return result;
    },
    [queryClient, generation, checkDisabled],
  );
}

/**
 * 对话里要显示的内容：
 * - turns：已落库的回合；有本页的运行时只取运行开始前的消息，运行的部分接在后面（发消息是新回合，继续是并进最后一个回合）；
 * - live：最后一个回合是否还在进行（过程不收起，显示进行中的条目与光标）；
 * - waiting：运行在等模型的下一段输出；
 * - end：最后一次运行的结束信息；本页没有记录时，最后一条消息是用户或工具消息就是“没有完成”；
 * - canContinue：落库内容确实没有完成（最后一条是用户或工具消息、会话空闲、本页没有运行），继续接口才不会得到 nothing_to_continue。
 */
export type ThreadView = {
  turns: Turn[];
  live: boolean;
  waiting: boolean;
  run: ActiveRun | null;
  end: RunEnd | { kind: "incomplete" } | null;
  canContinue: boolean;
  lastSequence: number;
};

export function threadOf(detail: ConversationDetailDTO | undefined, snapshot: RunSnapshot, id: string | null): ThreadView {
  const run = id === null ? null : (snapshot.runs.get(id) ?? null);
  const messages = detail?.messages ?? [];
  const base = run === null ? messages : messages.filter((message) => message.sequence <= run.baseSequence);
  const turns = turnsFromMessages(base);
  if (run !== null) {
    const last = turns.at(-1);
    if (run.userText !== null || last === undefined) {
      turns.push({ key: "live", userText: run.userText, items: run.view.items, texts: run.view.texts });
    } else {
      turns[turns.length - 1] = { ...last, items: [...last.items, ...run.view.items], texts: [...last.texts, ...run.view.texts] };
    }
  }
  const stored = id === null ? undefined : snapshot.ends.get(id);
  const canContinue = run === null && detail?.status === "idle" && isIncomplete(messages);
  let end: ThreadView["end"] = null;
  if (run === null || run.phase === "settling") {
    if (stored !== undefined) end = stored;
    else if (canContinue) end = { kind: "incomplete" };
  }
  const live = run !== null && run.phase !== "settling";
  return { turns, live, waiting: run !== null && live && run.view.waiting, run, end, canContinue, lastSequence: messages.at(-1)?.sequence ?? 0 };
}

export function useThread(id: string | null, detail: ConversationDetailDTO | undefined): ThreadView {
  return threadOf(detail, useRunSnapshot(), id);
}
