import { useEffect, useRef } from "react";
import { FailureNotice, Notice, Skeleton } from "../../../shared/ui";
import type { SettingsDTO, StewardMode } from "../api/decoders";
import { noDismissed, pageIdentityOf, playbackIdentityOf, requestContextOf, visibleContextOf } from "../context/context";
import { ContextChips } from "../context/context-chips";
import { usePageContext, usePlaybackContext } from "../context/host";
import {
  dockEpoch,
  draftOf,
  saveDraft,
  setDismissedContext,
  setNewConversationMode,
  setNewConversationModel,
  setViewNotice,
  useDismissedContext,
  useDockState,
  useDraft,
  useViewNotice,
} from "../dock-store";
import { isMissingConversation, useConversation, useCreateConversation, useMarkRead, usePatchConversation } from "../hooks/use-conversations";
import { placePlans, useConversationPlans, useReviewSnapshot } from "../hooks/use-plans";
import { useAttachRun, useRunSnapshot, useStartRun, useStewardDisabledCheck, useThread } from "../hooks/use-run";
import { effectiveModelOf, readinessOf } from "../hooks/use-steward-settings";
import { supersedePlans } from "../run/plan-store";
import { reviewViewOf } from "../run/review-store";
import { stopRun } from "../run/run-store";
import { markViewing } from "../run/viewing";
import { Composer } from "./composer";
import { ModeMenu, modeInfo, nextMode } from "./mode-menu";
import { ModelMenu } from "./model-menu";
import { PlanCard } from "./plan-card";
import { ReviewResult } from "./review-result";
import { SetupGuide } from "./setup-guide";
import { Thread } from "./thread";
import styles from "./thread.module.css";

type ConversationViewProps = {
  /** null 是空白的新对话：发第一条消息时才创建会话。 */
  conversationId: string | null;
  settings: SettingsDTO;
  name: string;
  /** 新对话创建后：面板记下这个会话，整页换成它的地址。 */
  onCreated: (id: string) => void;
};

const createMessages: Readonly<Record<string, string>> = {
  steward_not_configured: "还没有配置好模型，请先到设置里检查。",
  provider_no_tool_support: "当前模型不支持调用工具，请换一个模型。",
  steward_disabled: "管理员暂时关闭了成员使用管家。",
  conversation_busy: "这个对话正在回答或有等你确认的方案，现在不能切换模式。",
  // M7 D：自动整理的会话模式固定，后台方案由后台自己处理。
  curation_conversation: "自动整理的对话固定用“审查后执行”，不能切换模式。",
  curation_plan: "这是后台自动整理的方案，由后台处理；需要你决定的项在“自动整理”页的例外里。",
};

const noticeTitles = {
  create: "没能开始新对话",
  model: "没能切换模型",
  mode: "没能切换模式",
  new_model: "对话已经建好，但模型没有切换成功，消息还没有发出",
} as const;

/**
 * 一个会话的对话区（面板与整页共用）：对话内容、修改方案、结束说明与输入框。未配置时没有输入框，换成配置引导。
 * 调用方以会话 ID 为 key 渲染；草稿、草稿里点掉的上下文标签与提示按会话存在内存里，切回来还在。
 * 发消息时带上没被点掉的页面上下文（正在看、正在播放），发出后点掉的标签恢复；继续不带上下文。
 * 有未处理的方案时仍可发消息，发出即作废方案（C6）；模式在空白新对话里随创建会话提交，已有会话经 PATCH 切换，
 * 正在回答或有未处理的方案时不能切换。审查过（或正在审查）的方案显示为审查结果（C7），转给用户确认的项在其中确认。
 * C10：打开时标记已读；会话正在运行而本页没有这次运行（刷新、另一个标签页、重新打开）时接回它的事件流；
 * 挂载期间登记“正停在这个会话里”，运行结束时先标记已读再刷新未读数。
 */
export function ConversationView({ conversationId, settings, name, onCreated }: ConversationViewProps) {
  const detail = useConversation(conversationId);
  const snapshot = useRunSnapshot();
  const view = useThread(conversationId, detail.data);
  const attach = useAttachRun();
  const markRead = useMarkRead();
  const startRun = useStartRun();
  const checkDisabled = useStewardDisabledCheck();
  const create = useCreateConversation();
  const patch = usePatchConversation();
  const dock = useDockState();
  const draftKey = conversationId ?? "new";
  // 草稿与提示在 store 里：新对话创建后对话区按新会话重新挂载，之前的请求失败时写回的文字与提示照样显示。
  const draft = useDraft(draftKey);
  const failure = useViewNotice(draftKey);
  const setDraft = (text: string) => saveDraft(draftKey, text);
  const pageContext = usePageContext();
  const playbackContext = usePlaybackContext();
  const dismissed = useDismissedContext(draftKey);
  const shownContext = visibleContextOf(pageContext, playbackContext, dismissed);

  function dismissContext(key: "page" | "playback") {
    if (key === "page" && pageContext !== null) setDismissedContext(draftKey, { ...dismissed, page: pageIdentityOf(pageContext) });
    if (key === "playback" && playbackContext !== null) setDismissedContext(draftKey, { ...dismissed, playback: playbackIdentityOf(playbackContext) });
  }

  const readiness = readinessOf(settings);
  const running = view.run !== null;
  // 会话在运行而本页还没有接上（马上会接回），或接不回来了：不能发送。
  const elsewhere = !running && detail.data?.status === "running";
  const detached = elsewhere && conversationId !== null && snapshot.detached.get(conversationId) === detail.data?.updated_at;

  useEffect(() => (conversationId === null ? undefined : markViewing(conversationId)), [conversationId]);
  // 打开时标记已读一次（对话区按会话 ID 重新挂载，每次打开都会标）。
  const marked = useRef(false);
  const loaded = detail.data;
  useEffect(() => {
    if (conversationId === null || loaded === undefined || marked.current) return;
    marked.current = true;
    void markRead(conversationId, loaded.unread === true);
  }, [conversationId, loaded, markRead]);
  useEffect(() => {
    if (conversationId !== null && loaded !== undefined && loaded.status === "running" && !running) attach(conversationId, loaded);
  }, [conversationId, loaded, running, attach]);
  const creating = create.isPending;
  const plans = useConversationPlans(conversationId, detail.data, running);
  const placedPlans = placePlans(view.turns, plans);
  const reviews = useReviewSnapshot();
  const planOpen = plans.some((entry) => entry.phase === "pending" || entry.phase === "submitting");
  const mode: StewardMode = conversationId === null ? (dock.newMode ?? settings.default_mode) : (detail.data?.mode ?? "assistant");
  // 自动整理的会话（M7 D）：模式固定为 steward，后台的方案只读（需要人决定的项已成为例外）。
  const curation = conversationId !== null && detail.data?.kind === "curation";
  const modeLocked = running || elsewhere || planOpen || creating || patch.isPending || curation;

  async function send() {
    const text = draft.trim();
    if (text === "") return;
    const context = requestContextOf(pageContext, playbackContext, dismissed);
    setViewNotice(draftKey, null);
    setDraft("");
    setDismissedContext(draftKey, noDismissed);
    // 开流前失败：文字回到输入框（期间没有新输入时），点掉的标签也一起放回。
    const restore = (key: string) => {
      if (draftOf(key) !== "") return;
      saveDraft(key, text);
      setDismissedContext(key, dismissed);
    };
    // 每次异步返回后先核对：期间登出或换了用户（例如请求得到 401）时，草稿、提示与导航都属于上一个人，不再写。
    const started = dockEpoch();
    const stale = () => dockEpoch() !== started;
    if (conversationId !== null) {
      const result = await startRun(conversationId, text, view.lastSequence, context);
      if (stale()) return;
      // 后端收下新消息时把未处理的方案标为作废。
      if (result.started && planOpen) supersedePlans(conversationId, detail.data);
      if (!result.started && result.failure !== null) restore(draftKey);
      return;
    }
    let id: string;
    try {
      id = (await create.mutateAsync(dock.newMode ?? settings.default_mode)).id;
    } catch (error) {
      if (stale()) return;
      restore(draftKey);
      setViewNotice(draftKey, { action: "create", error });
      checkDisabled(error);
      return;
    }
    if (stale()) return;
    // 新会话默认回到 assistant：选过的模式已经写进这个会话。
    setNewConversationMode(null);
    const model = dock.newModel;
    setNewConversationModel(null);
    if (model !== null) {
      try {
        await patch.mutateAsync({ id, patch: { model } });
      } catch (error) {
        if (stale()) return;
        // 会话已经建好：换到它，消息先不发（模型不是用户选的那个），文字留在输入框里，提示模型没有切换成功。
        restore(id);
        setViewNotice(id, { action: "new_model", error });
        onCreated(id);
        return;
      }
      if (stale()) return;
    }
    // 先登记运行再换到新会话：新会话的对话区一出现就能看到这次运行。开流前被拒绝时文字回到新会话的输入框。
    const run = startRun(id, text, 0, context);
    onCreated(id);
    const result = await run;
    if (!stale() && !result.started && result.failure !== null) restore(id);
  }

  function continueRun() {
    if (conversationId === null) return;
    // 继续不带上下文：原用户消息的上下文保持发送时的样子。
    void startRun(conversationId, null, view.lastSequence, null);
  }

  function selectModel(model: string) {
    setViewNotice(draftKey, null);
    if (conversationId === null) {
      setNewConversationModel(model);
      return;
    }
    const started = dockEpoch();
    patch.mutate(
      { id: conversationId, patch: { model } },
      {
        onError: (error) => {
          if (dockEpoch() === started) setViewNotice(draftKey, { action: "model", error });
        },
      },
    );
  }

  function selectMode(next: StewardMode) {
    if (modeLocked || next === mode) return;
    setViewNotice(draftKey, null);
    if (conversationId === null) {
      setNewConversationMode(next === "assistant" ? null : next);
      return;
    }
    const started = dockEpoch();
    patch.mutate(
      { id: conversationId, patch: { mode: next } },
      {
        onError: (error) => {
          if (dockEpoch() === started) setViewNotice(draftKey, { action: "mode", error });
        },
      },
    );
  }

  if (conversationId !== null && detail.isPending) return <div className={styles.loading}><Skeleton label="正在读取对话" /></div>;
  if (conversationId !== null && detail.isError && detail.data === undefined) {
    const missing = isMissingConversation(detail.error);
    return (
      <div className={styles.loading}>
        {missing ? <Notice tone="warning">这个对话不存在或已经删除。</Notice> : <FailureNotice error={detail.error} title="对话没能读出来" />}
      </div>
    );
  }

  const ready = readiness === "ready";
  const currentModel = conversationId === null ? (dock.newModel ?? "") : (detail.data?.model ?? "");
  const conversationModel = currentModel === "" ? effectiveModelOf(settings) : currentModel;
  // 审查模型（C7）：空字符串表示与对话同一个模型，这时说明独立性有限（A8）。
  const reviewModel = settings.review_model ?? "";
  const sameReviewModel = reviewModel === "" || reviewModel === conversationModel;
  /** 会话详情里这份方案的完整内容（含审查已处理的项）。 */
  const storedPlanOf = (planId: string) =>
    detail.data?.pending_plan?.plan_id === planId ? detail.data.pending_plan : detail.data?.plans?.find((plan) => plan.plan_id === planId);
  const placeholder = view.turns.length === 0 ? `问问${name}` : `回复${name}`;
  return (
    <>
      <Thread
        view={view}
        busy={running}
        onContinue={continueRun}
        plans={placedPlans}
        renderPlan={(placed, turn) => {
          if (conversationId === null) return null;
          const planId = placed.entry.plan.plan_id;
          const stored = storedPlanOf(planId) ?? placed.entry.plan;
          const reviewState = reviews.get(planId);
          const review = reviewViewOf(reviewState, stored, running);
          if (review !== null) {
            return (
              <ReviewResult
                key={planId}
                conversationId={conversationId}
                entry={placed.entry}
                plan={stored}
                view={review}
                reviewState={reviewState}
                items={turn.items}
                baseSequence={view.lastSequence}
                busy={running || elsewhere || curation}
                resumable={!curation && view.canContinue && view.unfinishedPlans.has(planId)}
                onContinue={continueRun}
                name={name}
                conversationModel={conversationModel}
                mode={mode}
              />
            );
          }
          return (
            <PlanCard
              key={placed.entry.plan.plan_id}
              conversationId={conversationId}
              entry={placed.entry}
              items={turn.items}
              baseSequence={view.lastSequence}
              busy={running || elsewhere || curation}
              resumable={!curation && view.canContinue && view.unfinishedPlans.has(placed.entry.plan.plan_id)}
              onContinue={continueRun}
              name={name}
              autoApprove={mode === "assistant"}
            />
          );
        }}
        empty={ready ? <p className={styles.empty}>{`可以问${name}曲库里有什么、哪些专辑信息不全、最近扫描的情况。`}</p> : null}
        footer={
          <>
            {failure === null ? null : (
              <FailureNotice
                error={failure.error}
                context="write"
                messages={createMessages}
                title={noticeTitles[failure.action]}
              />
            )}
            {!ready && view.turns.length === 0 ? <SetupGuide readiness={readiness} name={name} /> : null}
          </>
        }
      />
      {ready ? (
        <Composer
          value={draft}
          onChange={setDraft}
          placeholder={placeholder}
          running={running}
          stopping={view.run?.phase === "stopping"}
          sendDisabled={elsewhere || creating}
          onSend={() => void send()}
          onStop={() => conversationId !== null && stopRun(conversationId)}
          note={detached ? "没能接上这次回答的进度，可以刷新页面查看最新的结果。" : undefined}
          hint={
            curation
              ? "这是后台自动整理的对话；你在这里发的消息按你本人的对话处理，改动写进人工修正"
              : planOpen
              ? "发新消息会作废上面的方案"
              : view.unfinishedPlans.size > 0 && view.canContinue
                ? "发新消息不会执行方案里剩下的项"
                : mode === "steward" && sameReviewModel
                  ? `${modeInfo.steward.hint}；审查与对话是同一个模型，独立性有限`
                  : modeInfo[mode].hint
          }
          onShiftTab={modeLocked ? undefined : () => selectMode(nextMode(mode))}
          context={<ContextChips page={shownContext.page} playback={shownContext.playback} onDismiss={dismissContext} />}
          tools={
            <>
            {curation ? null : <ModeMenu current={mode} disabled={modeLocked} onSelect={selectMode} />}
            <ModelMenu
              current={currentModel}
              defaultModel={effectiveModelOf(settings)}
              models={settings.probe_result?.models ?? []}
              reviewModel={mode === "steward" ? reviewModel : null}
              disabled={patch.isPending}
              onSelect={selectModel}
            />
            </>
          }
        />
      ) : view.turns.length === 0 ? null : (
        <div className={styles.guideDock}>
          <SetupGuide readiness={readiness} name={name} compact />
        </div>
      )}
    </>
  );
}
