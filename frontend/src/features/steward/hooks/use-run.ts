import { useQueryClient } from "@tanstack/react-query";
import { isHttpFailure } from "../../../shared/api";
import { useCallback, useSyncExternalStore } from "react";
import { useViewerScope } from "../../auth";
import { stewardKeys } from "../query-keys";
import type { ConversationDetailDTO } from "../api/decoders";
import type { StewardRequestContext } from "../context/context";
import { useStewardHost } from "../context/host";
import { isIncomplete, turnsFromMessages, type Turn } from "../run/reduce";
import { rememberPlan, unfinishedPlanIdsOf } from "../run/plan-store";
import { reviewFinished, reviewStarted, settleReviews } from "../run/review-store";
import { getRunSnapshot, startRun, subscribeRuns, type ActiveRun, type RunEnd, type RunSnapshot, type StartResult, type StartRunParams } from "../run/run-store";
import { refreshConversation } from "../run/refresh";
import { toolDataScopeOf } from "../tool-labels";
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

/**
 * 发消息（text）或从现有历史继续（null），context 是随消息发的页面上下文（继续时为 null）；action 是对一份方案的决定（C6）
 * 或重新审查（C7），text 为 null。
 * 运行结束后重读会话与列表；改了收藏、歌单、播放历史或曲库的工具每结束一次，就让那类数据的查询失效（播放类不用，分区事件会推过来）。
 * 运行里出现的修改方案记进方案的 store；审查的开始与结论记进审查的 store，开始审查时重读会话，取得送审方案的各项（审查事件里只有方案 ID）。
 */
export function useStartRun() {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const checkDisabled = useStewardDisabledCheck();
  const { refreshData } = useStewardHost();
  return useCallback(
    async (
      conversationId: string,
      text: string | null,
      baseSequence: number,
      context: StewardRequestContext | null,
      action: Pick<StartRunParams, "decision" | "review"> = {},
    ): Promise<StartResult> => {
      const result = await startRun({
        conversationId,
        text,
        baseSequence,
        context,
        refresh: () => refreshConversation(queryClient, generation, conversationId),
        onToolFinished: (name) => {
          const scope = toolDataScopeOf(name);
          if (scope !== null) refreshData(scope);
        },
        onPlan: (plan) => rememberPlan(conversationId, plan),
        // 审查中被取消或保存失败时流以 done 或断开结束、没有 review_finished：不再显示“审查中”，以重读的落库内容为准。
        onEnd: () => settleReviews(conversationId),
        onReview: (event) => {
          if (event.type === "review_finished") {
            reviewFinished(conversationId, event.review);
            return;
          }
          reviewStarted(conversationId, event.plan_id, event.model);
          void refreshConversation(queryClient, generation, conversationId).catch(() => undefined);
        },
        ...action,
      });
      if (!result.started && isHttpFailure(result.failure, 403, "steward_disabled")) checkDisabled(null, true);
      return result;
    },
    [queryClient, generation, checkDisabled, refreshData],
  );
}

/**
 * 对话里要显示的内容：
 * - turns：已落库的回合；有本页的运行时只取运行开始前的消息，运行的部分接在后面（发消息是新回合，继续是并进最后一个回合）；
 * - live：最后一个回合是否还在进行（过程不收起，显示进行中的条目与光标）；
 * - waiting：运行在等模型的下一段输出；
 * - end：最后一次运行的结束信息；本页没有记录时，最后一条消息是用户或工具消息就是“没有完成”；
 * - canContinue：落库内容确实没有完成（最后一条是用户或工具消息，或有已确认但还没执行完的方案；会话空闲、本页没有运行），
 *   继续接口才不会得到 nothing_to_continue；
 * - unfinishedPlans：已确认但还没执行完的方案（C6），由方案卡片给出“继续”，结束说明不再重复这个按钮。
 */
export type ThreadView = {
  turns: Turn[];
  live: boolean;
  waiting: boolean;
  run: ActiveRun | null;
  end: RunEnd | { kind: "incomplete" } | null;
  canContinue: boolean;
  unfinishedPlans: ReadonlySet<string>;
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
      // 继续或确认方案：并进最后一个回合。同一个调用（确认后执行的方案项）用运行里的状态替换落库的，位置不变；
      // 新的调用排在后面，它们之前的文字段数加上回合里已有的。
      const live = new Map(run.view.items.map((item) => [item.callId, item]));
      const items = last.items.map((item) => {
        const current = live.get(item.callId);
        if (current === undefined) return item;
        live.delete(item.callId);
        return { ...current, textsBefore: item.textsBefore };
      });
      for (const item of live.values()) items.push({ ...item, textsBefore: item.textsBefore + last.texts.length });
      turns[turns.length - 1] = { ...last, items, texts: [...last.texts, ...run.view.texts] };
    }
  }
  const stored = id === null ? undefined : snapshot.ends.get(id);
  const unfinishedPlans = run === null ? unfinishedPlanIdsOf(detail) : new Set<string>();
  const canContinue = run === null && detail?.status === "idle" && (isIncomplete(messages) || unfinishedPlans.size > 0);
  let end: ThreadView["end"] = null;
  if (run === null || run.phase === "settling") {
    if (stored !== undefined) end = stored;
    else if (canContinue) end = { kind: "incomplete" };
  }
  const live = run !== null && run.phase !== "settling";
  return { turns, live, waiting: run !== null && live && run.view.waiting, run, end, canContinue, unfinishedPlans, lastSequence: messages.at(-1)?.sequence ?? 0 };
}

export function useThread(id: string | null, detail: ConversationDetailDTO | undefined): ThreadView {
  return threadOf(detail, useRunSnapshot(), id);
}
