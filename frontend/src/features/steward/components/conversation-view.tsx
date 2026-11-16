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
import { isMissingConversation, useConversation, useCreateConversation, usePatchConversation } from "../hooks/use-conversations";
import { placePlans, useConversationPlans } from "../hooks/use-plans";
import { useStartRun, useStewardDisabledCheck, useThread } from "../hooks/use-run";
import { effectiveModelOf, readinessOf } from "../hooks/use-steward-settings";
import { supersedePlans } from "../run/plan-store";
import { stopRun } from "../run/run-store";
import { Composer } from "./composer";
import { ModeMenu, modeInfo, nextMode } from "./mode-menu";
import { ModelMenu } from "./model-menu";
import { PlanCard } from "./plan-card";
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
 * 正在回答或有未处理的方案时不能切换。
 */
export function ConversationView({ conversationId, settings, name, onCreated }: ConversationViewProps) {
  const detail = useConversation(conversationId);
  const view = useThread(conversationId, detail.data);
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
  const elsewhere = !running && detail.data?.status === "running";
  const creating = create.isPending;
  const plans = useConversationPlans(conversationId, detail.data, running);
  const placedPlans = placePlans(view.turns, plans);
  const planOpen = plans.some((entry) => entry.phase === "pending" || entry.phase === "submitting");
  const mode: StewardMode = conversationId === null ? (dock.newMode ?? "assistant") : (detail.data?.mode ?? "assistant");
  const modeLocked = running || elsewhere || planOpen || creating || patch.isPending;

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
      id = (await create.mutateAsync(dock.newMode ?? "assistant")).id;
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
  const placeholder = view.turns.length === 0 ? `问问${name}` : `回复${name}`;
  return (
    <>
      <Thread
        view={view}
        busy={running}
        onContinue={continueRun}
        plans={placedPlans}
        renderPlan={(placed, turn) =>
          conversationId === null ? null : (
            <PlanCard
              key={placed.entry.plan.plan_id}
              conversationId={conversationId}
              entry={placed.entry}
              items={turn.items}
              baseSequence={view.lastSequence}
              busy={running || elsewhere}
              resumable={view.canContinue && view.unfinishedPlans.has(placed.entry.plan.plan_id)}
              onContinue={continueRun}
              name={name}
            />
          )
        }
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
          note={elsewhere ? "正在别处回答，结束后才能继续发消息。" : undefined}
          hint={
            planOpen ? "发新消息会作废上面的方案" : view.unfinishedPlans.size > 0 && view.canContinue ? "发新消息不会执行方案里剩下的项" : modeInfo[mode].hint
          }
          onShiftTab={modeLocked ? undefined : () => selectMode(nextMode(mode))}
          context={<ContextChips page={shownContext.page} playback={shownContext.playback} onDismiss={dismissContext} />}
          tools={
            <>
            <ModeMenu current={mode} disabled={modeLocked} onSelect={selectMode} />
            <ModelMenu
              current={currentModel}
              defaultModel={effectiveModelOf(settings)}
              models={settings.probe_result?.models ?? []}
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
