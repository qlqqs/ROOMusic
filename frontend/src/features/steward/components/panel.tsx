import { useNavigate } from "@tanstack/react-router";
import { Maximize2, Plus, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { Button, FailureNotice, Skeleton } from "../../../shared/ui";
import { clearDockFocus, setDockConversation, setDockOpen, startBlankConversation, useDockState } from "../dock-store";
import { isMissingConversation, useConversation } from "../hooks/use-conversations";
import { useStewardEntry, useStewardSettings } from "../hooks/use-steward-settings";
import { titleOf } from "./conversation-actions";
import { ConversationView } from "./conversation-view";
import styles from "./panel.module.css";

/**
 * 右侧停靠面板：标题栏是名字与当前对话的标题，右边是新对话、展开到整页、收起。
 * 与整页是同一个会话的两种显示（当前会话记在面板状态里），切页面不打断进行中的回答。
 * 由用户打开时，内容就绪后焦点移进输入框（没有输入框时移到面板本身），加载期间焦点已被挪走（canTakeFocus 为假）就不移；收起由外壳的 onClose 处理（焦点回到侧栏入口）。
 * 记着的会话已经不存在时回到空白新对话。
 */
export function StewardPanel({ id, onClose, canTakeFocus }: { id: string; onClose: () => void; canTakeFocus: () => boolean }) {
  const dock = useDockState();
  const settings = useStewardSettings();
  const { name } = useStewardEntry();
  const conversation = useConversation(dock.conversationId);
  const navigate = useNavigate();
  const panel = useRef<HTMLElement>(null);
  const title = dock.conversationId === null || conversation.data === undefined ? "" : titleOf(conversation.data);
  const missing = dock.conversationId !== null && isMissingConversation(conversation.error);
  const contentReady = settings.status !== "pending" && (dock.conversationId === null || conversation.status !== "pending");

  useEffect(() => {
    if (missing) setDockConversation(null);
  }, [missing]);
  useEffect(() => {
    if (!dock.focusPending || !contentReady) return;
    // 加载期间用户已经把焦点挪去别处时不抢。
    if (canTakeFocus()) (panel.current?.querySelector("textarea") ?? panel.current)?.focus();
    clearDockFocus();
  }, [dock.focusPending, contentReady, canTakeFocus]);

  const expand = () => {
    setDockOpen(false);
    if (dock.conversationId === null) {
      startBlankConversation();
      void navigate({ to: "/steward" });
    } else void navigate({ to: "/steward/$conversationId", params: { conversationId: dock.conversationId } });
  };
  return (
    <aside ref={panel} id={id} className={styles.panel} aria-label={name} tabIndex={-1}>
      <header className={styles.head}>
        <h2 className={styles.name}>{name}</h2>
        <span className={styles.session}>{title}</span>
        {settings.data === undefined ? null : <Button variant="icon" icon={<Plus size={18} />} label="新对话" onClick={() => setDockConversation(null)} />}
        <Button variant="icon" icon={<Maximize2 size={16} />} label="展开到整页" onClick={expand} />
        <Button variant="icon" icon={<X size={18} />} label="收起" onClick={onClose} />
      </header>
      {settings.data !== undefined ? (
        <ConversationView key={dock.conversationId ?? "new"} conversationId={dock.conversationId} settings={settings.data} name={name} onCreated={setDockConversation} />
      ) : settings.isError ? (
        <div className={styles.state}>
          <FailureNotice error={settings.error} title="设置没能读出来" />
        </div>
      ) : (
        <div className={styles.state}>
          <Skeleton label="正在读取设置" />
        </div>
      )}
    </aside>
  );
}
