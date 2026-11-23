import { useNavigate } from "@tanstack/react-router";
import { Menu, Plus, Settings } from "lucide-react";
import { useEffect, useState } from "react";
import { usePageTitle } from "../../../shared/lib/use-page-title";
import { useMediaQuery } from "../../../shared/lib/use-media-query";
import { Button, EmptyState, FailureNotice, MenuButton, Skeleton, StatusTag } from "../../../shared/ui";
import { getDockState, setDockConversation, startBlankConversation, useDockState } from "../dock-store";
import { isMissingConversation, useConversation, useConversations } from "../hooks/use-conversations";
import { useRuns } from "../hooks/use-run";
import { DEFAULT_STEWARD_NAME, isStewardDisabled, useStewardEntry, useStewardSettings } from "../hooks/use-steward-settings";
import { titleOf, useConversationActions } from "./conversation-actions";
import { ConversationView } from "./conversation-view";
import { SessionList } from "./session-list";
import styles from "./steward-page.module.css";

/** 与外壳一致：窄于 768px 时对话与列表分两屏。 */
const NARROW = "(max-width: 767px)";

/**
 * 整页对话（/steward 与 /steward/$conversationId）：左边是会话列表，右边是对话。手机上两者分两屏，标题栏左侧的按钮切换。
 * /steward 是“最近的会话或空白新对话”：面板记着会话时转到它；没有时（例如刷新之后）转到列表里最近的一个；没有会话、
 * 或用户在应用里要的就是空白新对话（“新对话”、标签栏）时留在空白新对话。
 * 地址里的会话读取成功后记进面板状态，收起整页后面板接着显示它；不存在的不记。记着的会话已经不存在时回到空白新对话。
 */
export function StewardPage({ conversationId }: { conversationId: string | null }) {
  const settings = useStewardSettings();
  const { name } = useStewardEntry();
  const dock = useDockState();
  const detail = useConversation(conversationId);
  const list = useConversations();
  const navigate = useNavigate();
  const narrow = useMediaQuery(NARROW);
  const [showList, setShowList] = useState(false);
  usePageTitle(name);

  // 自动整理的会话（M7 D）不在对话列表里，不记进面板，免得之后打开管家跳到它。
  const loaded = conversationId !== null && detail.data !== undefined && detail.data.kind !== "curation";
  // 重读时才发现不存在（别处删了）也算：缓存里可能还有旧数据。
  const missing = conversationId !== null && isMissingConversation(detail.error);
  useEffect(() => {
    if (loaded) setDockConversation(conversationId);
  }, [loaded, conversationId]);
  useEffect(() => {
    if (!missing || getDockState().conversationId !== conversationId) return;
    startBlankConversation();
    void navigate({ to: "/steward", replace: true });
  }, [missing, conversationId, navigate]);

  const latest = list.data?.items[0]?.id;
  const redirectTo = conversationId !== null ? null : (dock.conversationId ?? (dock.blankIntent ? null : (latest ?? null)));
  const waitingForList = conversationId === null && dock.conversationId === null && !dock.blankIntent && list.isPending;
  useEffect(() => {
    if (redirectTo !== null) void navigate({ to: "/steward/$conversationId", params: { conversationId: redirectTo }, replace: true });
  }, [redirectTo, navigate]);

  const startNew = () => {
    startBlankConversation();
    setShowList(false);
    void navigate({ to: "/steward" });
  };
  const onDeleted = (id: string) => {
    if (id === conversationId) startNew();
  };

  // 先看是不是被关闭了：之前读到过设置、之后重读被拒绝时，旧的设置数据还在。
  if (isStewardDisabled(settings.error)) {
    return (
      <div className={styles.message}>
        <h1 className={styles.visuallyHidden}>{DEFAULT_STEWARD_NAME}</h1>
        <EmptyState icon={<Settings size={24} />} title="管理员暂时关闭了这个功能">
          成员现在不能使用{DEFAULT_STEWARD_NAME}。需要时请联系管理员在设置里打开。
        </EmptyState>
      </div>
    );
  }
  if (settings.data === undefined) {
    return (
      <div className={styles.message}>
        <h1 className={styles.visuallyHidden}>{name}</h1>
        {settings.isError ? <FailureNotice error={settings.error} title="设置没能读出来" /> : <Skeleton label="正在读取设置" />}
      </div>
    );
  }

  const listScreen = narrow && showList;
  const redirecting = redirectTo !== null || waitingForList;
  return (
    <div className={styles.page} data-screen={listScreen ? "list" : "chat"}>
      {narrow && !showList ? null : (
        <div className={styles.listPane}>
          {narrow ? (
            <header className={styles.head}>
              <h1 className={styles.title}>{name}</h1>
              <Button variant="icon" icon={<Plus size={20} />} label="新对话" onClick={startNew} />
            </header>
          ) : null}
          <SessionList currentId={conversationId} onNew={startNew} onDeleted={onDeleted} onPicked={() => setShowList(false)} showNew={!narrow} />
        </div>
      )}
      {listScreen ? null : (
        <section className={styles.conversation} aria-label="对话">
          <ConversationHeader
            conversationId={conversationId}
            name={name}
            narrow={narrow}
            onShowList={() => setShowList(true)}
            onNew={startNew}
            onDeleted={onDeleted}
          />
          {redirecting ? (
            <div className={styles.message}>
              <Skeleton label="正在打开最近的对话" />
            </div>
          ) : (
            <ConversationView
              key={conversationId ?? "new"}
              conversationId={conversationId}
              settings={settings.data}
              name={name}
              onCreated={(id) => void navigate({ to: "/steward/$conversationId", params: { conversationId: id }, replace: true })}
            />
          )}
        </section>
      )}
    </div>
  );
}

/** 对话的标题栏：标题（页面的 h1），宽屏右侧是改名与删除，手机上左侧是“全部对话”、右侧是“新对话”。 */
function ConversationHeader({
  conversationId,
  name,
  narrow,
  onShowList,
  onNew,
  onDeleted,
}: {
  conversationId: string | null;
  name: string;
  narrow: boolean;
  onShowList: () => void;
  onNew: () => void;
  onDeleted: (id: string) => void;
}) {
  const conversation = useConversation(conversationId);
  const runs = useRuns();
  const { actionsFor, dialogs } = useConversationActions({ onDeleted });
  const data = conversationId === null ? undefined : conversation.data;
  const title = conversationId === null ? "新对话" : data === undefined ? "" : titleOf(data);
  return (
    <header className={styles.head}>
      {narrow ? <Button variant="icon" icon={<Menu size={20} />} label="全部对话" onClick={onShowList} /> : null}
      <h1 className={styles.title}>{title === "" ? name : title}</h1>
      {data?.kind === "curation" ? <StatusTag tone="neutral">自动整理</StatusTag> : null}
      {narrow ? <Button variant="icon" icon={<Plus size={20} />} label="新对话" onClick={onNew} /> : null}
      {!narrow && data !== undefined ? (
        <MenuButton label="对话选项" icon={<Settings size={18} />} iconOnly actions={actionsFor(data, runs.has(data.id) || data.status === "running")} />
      ) : null}
      {dialogs}
    </header>
  );
}
