import { Link } from "@tanstack/react-router";
import { MoreHorizontal, Plus } from "lucide-react";
import { Button, FailureNotice, MenuButton, Skeleton } from "../../../shared/ui";
import type { ConversationDTO } from "../api/decoders";
import { useConversations } from "../hooks/use-conversations";
import { useRuns } from "../hooks/use-run";
import { titleOf, useConversationActions } from "./conversation-actions";
import styles from "./session-list.module.css";

type Group = { label: string; items: ConversationDTO[] };

/** 当天零点与本周一零点（浏览器所在时区）。 */
function boundaries(now: Date): { today: number; week: number } {
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const weekday = (today.getDay() + 6) % 7;
  return { today: today.getTime(), week: today.getTime() - weekday * 24 * 60 * 60 * 1000 };
}

/** 按最后活动时间分成今天、本周、更早；空的组不出现。列表本身已按时间倒序。 */
export function groupConversations(items: readonly ConversationDTO[], now: Date = new Date()): Group[] {
  const { today, week } = boundaries(now);
  const groups: Group[] = [
    { label: "今天", items: [] },
    { label: "本周", items: [] },
    { label: "更早", items: [] },
  ];
  for (const item of items) {
    const time = Date.parse(item.updated_at);
    const group = time >= today ? groups[0] : time >= week ? groups[1] : groups[2];
    group?.items.push(item);
  }
  return groups.filter((group) => group.items.length > 0);
}

type SessionListProps = {
  currentId: string | null;
  onNew: () => void;
  /** 删除的是当前会话时，调用方换到空白新对话。 */
  onDeleted: (id: string) => void;
  /** 点了某个会话（手机上据此切回对话屏）。 */
  onPicked?: () => void;
  /** 手机的列表屏标题栏里另有“新对话”，这里不再放。 */
  showNew?: boolean;
};

/** 整页左侧的会话列表：新对话、按时间分组的会话（正在回答的有标记），每项可以改名、删除。 */
export function SessionList({ currentId, onNew, onDeleted, onPicked, showNew = true }: SessionListProps) {
  const query = useConversations();
  const runs = useRuns();
  const { actionsFor, dialogs } = useConversationActions({ onDeleted });
  return (
    <nav className={styles.sessions} aria-label="对话">
      {showNew ? (
        <Button variant="primary" icon={<Plus size={16} />} onClick={onNew}>
          新对话
        </Button>
      ) : null}
      {query.isPending ? <Skeleton label="正在读取对话" lines={4} /> : null}
      {query.isError && query.data === undefined ? <FailureNotice error={query.error} title="对话列表没能读出来" /> : null}
      {query.data !== undefined && query.data.items.length === 0 ? <p className={styles.none}>还没有对话。</p> : null}
      {query.data === undefined
        ? null
        : groupConversations(query.data.items).map((group) => (
            <section key={group.label} className={styles.group} aria-label={group.label}>
              <h3 className={styles.groupLabel}>{group.label}</h3>
              <ul className={styles.list}>
                {group.items.map((item) => {
                  const running = runs.has(item.id) || item.status === "running";
                  return (
                    <li key={item.id} className={styles.row}>
                      <Link
                        to="/steward/$conversationId"
                        params={{ conversationId: item.id }}
                        className={styles.item}
                        aria-current={item.id === currentId ? "page" : undefined}
                        onClick={onPicked}
                      >
                        <span className={styles.title}>{titleOf(item)}</span>
                        {running ? <small className={styles.state}>正在回答</small> : null}
                      </Link>
                      <span className={styles.more}>
                        <MenuButton label={`“${titleOf(item)}”的操作`} icon={<MoreHorizontal size={16} />} iconOnly actions={actionsFor(item, running)} />
                      </span>
                    </li>
                  );
                })}
              </ul>
            </section>
          ))}
      {dialogs}
    </nav>
  );
}
