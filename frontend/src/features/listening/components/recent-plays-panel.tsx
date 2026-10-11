import { useEffect, useRef, useState } from "react";
import { Button, Notice, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useClearPlayHistory } from "../hooks/use-clear-play-history";
import { useRecentReleases } from "../hooks/use-recent-releases";
import { ClearHistoryDialog } from "./clear-history-dialog";
import { RecentReleaseCard } from "./recent-release-card";
import styles from "./recent-plays-panel.module.css";

/** 空态说明计次规则：用户才知道为什么刚听过的曲目还没出现。 */
const EMPTY_TEXT = "还没有播放记录。一首曲目播放到一半（最长 4 分钟）就会记在这里。";
/** 有记录、但播放过的曲目目前都不在资料库里（文件缺失）：不显示它们，但用户仍可以清除。 */
const HIDDEN_TEXT = "没有可显示的播放记录：播放过的曲目目前都不在资料库里。";

/**
 * 概览的“最近播放”区块：当前用户最近播放的专辑（最多 8 张，每张一次，最近的在前）与清除自己全部历史的按钮。
 * 记录只属于当前用户，清除前需要确认；清除成功后焦点回到区块标题（打开对话框的按钮已经不存在了），并用状态提示说明结果。
 */
export function RecentPlaysPanel() {
  const recent = useRecentReleases();
  const clear = useClearPlayHistory();
  const [confirming, setConfirming] = useState(false);
  const [cleared, setCleared] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusHeadingOnClose = useRef(false);

  useEffect(() => {
    // 对话框的清理先把焦点还给打开它的按钮；清除成功后那个按钮已经消失，这里再把焦点带到标题。
    if (!confirming && focusHeadingOnClose.current) {
      focusHeadingOnClose.current = false;
      headingRef.current?.focus();
    }
  }, [confirming]);

  const hasHistory = recent.data?.has_history === true;
  const close = () => setConfirming(false);

  return (
    <>
      <Panel title="最近播放" headingRef={headingRef} action={hasHistory ? <Button onClick={() => setConfirming(true)}>清除播放历史</Button> : undefined}>
        {cleared && !hasHistory ? <Notice tone="success">已清除你的播放历史。</Notice> : null}
        <QueryContent query={recent} loading={<Skeleton label="正在加载最近播放" lines={3} />}>
          {(data) =>
            data.items.length === 0 ? (
              <p>{data.has_history ? HIDDEN_TEXT : EMPTY_TEXT}</p>
            ) : (
              <ul className={styles.grid}>
                {data.items.map((item) => (
                  <RecentReleaseCard key={item.release.id} item={item} />
                ))}
              </ul>
            )
          }
        </QueryContent>
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
