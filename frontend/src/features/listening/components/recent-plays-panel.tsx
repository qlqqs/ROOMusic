import { Link } from "@tanstack/react-router";
import { ChevronRight, MoreHorizontal, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { MenuButton, Notice, Panel, QueryContent, Skeleton, useShelf } from "../../../shared/ui";
import type { RecentReleaseDTO } from "../api/decoders";
import { useClearPlayHistory } from "../hooks/use-clear-play-history";
import { useRecentReleases } from "../hooks/use-recent-releases";
import { PLAY_COUNT_RULE_TEXT } from "../format";
import { ClearHistoryDialog } from "./clear-history-dialog";
import { RecentReleaseCard } from "./recent-release-card";
import styles from "./recent-plays-panel.module.css";

/** 有记录、但播放过的曲目目前都不在资料库里（文件缺失）：不显示它们，但用户仍可以清除。 */
const HIDDEN_TEXT = "没有可显示的播放记录：播放过的曲目目前都不在资料库里。";

type RecentPlaysPanelProps = {
  /** 每张卡片封面上的操作（播放键）：播放要用 player，由 app 组合，listening 不导入 player。 */
  renderCardAction?: (item: RecentReleaseDTO) => ReactNode;
  /** 卡片操作的结果（失败、队列已满），放在横排下方；没有内容时不占位。 */
  status?: ReactNode;
};

/**
 * 概览的“最近播放”区块：当前用户最近播放的专辑（最多 8 张，每张一次，最近的在前）一行横排，放不下时横向滚动、标题行有翻页按钮；
 * 标题行还有去历史页的“全部历史”与“更多”菜单（里面是“清除播放历史”）。
 * 有任何记录（含文件缺失、卡片里不显示的）时才有“全部历史”与“更多”：历史页仍会列出缺失的曲目，可以逐首移除。
 * 记录只属于当前用户，清除前需要确认；清除成功后焦点回到区块标题（打开对话框的菜单按钮已经不存在了），并用状态提示说明结果。
 * 从来没有播放记录时整块不出现（概览上不留一块只有一句空态的区块）；计次规则写在历史页的空态里。
 */
export function RecentPlaysPanel({ renderCardAction, status }: RecentPlaysPanelProps) {
  const recent = useRecentReleases();
  const clear = useClearPlayHistory();
  const shelf = useShelf("最近播放");
  const [confirming, setConfirming] = useState(false);
  const [cleared, setCleared] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusHeadingOnClose = useRef(false);

  useEffect(() => {
    // 对话框的清理先把焦点还给打开它之前的焦点；清除成功后“更多”按钮已经消失，这里再把焦点带到标题。
    if (!confirming && focusHeadingOnClose.current) {
      focusHeadingOnClose.current = false;
      headingRef.current?.focus();
    }
  }, [confirming]);

  const hasHistory = recent.data?.has_history === true;
  const close = () => setConfirming(false);
  if (recent.data !== undefined && !recent.data.has_history && recent.data.items.length === 0 && !cleared && !confirming) return null;

  return (
    <>
      <Panel
        title="最近播放"
        headingRef={headingRef}
        action={
          hasHistory ? (
            <span className={styles.actions}>
              <Link to="/me/history" search={{}} className={styles.all}>
                全部历史
                <ChevronRight size={16} aria-hidden="true" />
              </Link>
              <MenuButton
                label="最近播放的更多操作"
                iconOnly
                icon={<MoreHorizontal size={18} />}
                actions={[{ id: "clear", label: "清除播放历史", icon: <Trash2 size={16} />, onSelect: () => setConfirming(true) }]}
              />
              {shelf.controls}
            </span>
          ) : undefined
        }
      >
        {cleared && !hasHistory ? <Notice tone="success">已清除你的播放历史。</Notice> : null}
        <QueryContent query={recent} loading={<Skeleton label="正在加载最近播放" lines={3} />}>
          {(data) =>
            data.items.length === 0 ? (
              <p>{data.has_history ? HIDDEN_TEXT : PLAY_COUNT_RULE_TEXT}</p>
            ) : (
              <ul className={shelf.className} ref={shelf.ref}>
                {data.items.map((item) => (
                  <RecentReleaseCard key={item.release.id} item={item} action={renderCardAction?.(item)} />
                ))}
              </ul>
            )
          }
        </QueryContent>
        {status}
      </Panel>
      <ClearHistoryDialog
        open={confirming}
        state={clear.state}
        onConfirm={() =>
          clear.clear({
            onSucceeded: () => {
              setCleared(true);
              focusHeadingOnClose.current = true;
              close();
            },
          })
        }
        onCancel={() => {
          clear.dismiss();
          close();
        }}
      />
    </>
  );
}
