import { FailureNotice, Notice, Skeleton } from "../../../shared/ui";
import type { SettingsDTO } from "../api/decoders";
import { noDismissed, pageIdentityOf, playbackIdentityOf, requestContextOf, visibleContextOf } from "../context/context";
import { ContextChips } from "../context/context-chips";
import { usePageContext, usePlaybackContext } from "../context/host";
import {
  dockEpoch,
  draftOf,
  saveDraft,
  setDismissedContext,
  setNewConversationModel,
  setViewNotice,
  useDismissedContext,
  useDockState,
  useDraft,
  useViewNotice,
} from "../dock-store";
import { isMissingConversation, useConversation, useCreateConversation, usePatchConversation } from "../hooks/use-conversations";
import { useStartRun, useStewardDisabledCheck, useThread } from "../hooks/use-run";
import { effectiveModelOf, readinessOf } from "../hooks/use-steward-settings";
import { stopRun } from "../run/run-store";
import { Composer } from "./composer";
import { ModelMenu } from "./model-menu";
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
};

const noticeTitles = {
  create: "没能开始新对话",
  model: "没能切换模型",
  new_model: "对话已经建好，但模型没有切换成功，消息还没有发出",
} as const;

/**
 * 一个会话的对话区（面板与整页共用）：对话内容、结束说明与输入框。未配置时没有输入框，换成配置引导。
 * 调用方以会话 ID 为 key 渲染；草稿、草稿里点掉的上下文标签与提示按会话存在内存里，切回来还在。
 * 发消息时带上没被点掉的页面上下文（正在看、正在播放），发出后点掉的标签恢复；继续不带上下文。
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
      if (!stale() && !result.started && result.failure !== null) restore(draftKey);
      return;
    }
    let id: string;
    try {
      id = (await create.mutateAsync()).id;
    } catch (error) {
      if (stale()) return;
      restore(draftKey);
      setViewNotice(draftKey, { action: "create", error });
      checkDisabled(error);
      return;
    }
    if (stale()) return;
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
          context={<ContextChips page={shownContext.page} playback={shownContext.playback} onDismiss={dismissContext} />}
          tools={
            <ModelMenu
              current={currentModel}
              defaultModel={effectiveModelOf(settings)}
              models={settings.probe_result?.models ?? []}
              disabled={patch.isPending}
              onSelect={selectModel}
            />
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
