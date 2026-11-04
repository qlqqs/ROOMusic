import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, DataTable, Notice, Pagination, Panel, QueryContent, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import type { HistoryItemDTO } from "../api/decoders";
import { historyArtistOf, historyTitleOf, PLAY_COUNT_RULE_TEXT } from "../format";
import { useClearPlayHistory } from "../hooks/use-clear-play-history";
import { useHistoryTracks } from "../hooks/use-history-tracks";
import { useRemoveHistoryTrack } from "../hooks/use-remove-history-track";
import { ClearHistoryDialog } from "./clear-history-dialog";
import { RemoveTrackDialog } from "./remove-track-dialog";
import styles from "./history-view.module.css";

type HistoryViewProps = {
  /** URL 里的页码，从 1 起。 */
  page: number;
  /** 顶部的“播放全部”：由 app 组合播放器动作（listening 不导入 player）；只在有历史时显示。 */
  renderPlayAll: () => ReactNode;
  /** 一行的播放按钮、心形与“更多”菜单：由 app 组合播放器与收藏动作；requestRemove 打开这里的移除确认。 */
  renderTrackActions: (item: HistoryItemDTO, requestRemove: () => void) => ReactNode;
  /** 包住这一页表格的范围（例如一次读出这一页的收藏状态）：由 app 组合，listening 不知道里面是什么。 */
  renderRowsScope?: ((items: readonly HistoryItemDTO[], children: ReactNode) => ReactNode) | undefined;
};

/** 结果提示：移除或清除成功后显示在区块顶部，换页后不再显示移除的提示。 */
type Outcome = { kind: "removed"; title: string; page: number } | { kind: "cleared" } | null;

/** 文件缺失的行整行灰色；“文件缺失”标签用文字表达同一件事，不只靠颜色。 */
function Muted({ item, children }: { item: HistoryItemDTO; children: ReactNode }) {
  return <div className={item.present ? undefined : styles.missing}>{children}</div>;
}

/** 时间：日期与时分各自一段、不在中间换行；两段之间仍是一个空格，文字内容与 formatDateTime 的结果相同。 */
function PlayedAt({ value }: { value: string }) {
  const text = formatDateTime(value);
  const gap = text.indexOf(" ");
  return (
    <time dateTime={value}>
      {gap < 0 ? (
        text
      ) : (
        <>
          <span className={styles.timePart}>{text.slice(0, gap)}</span> <span className={styles.timePart}>{text.slice(gap + 1)}</span>
        </>
      )}
    </time>
  );
}

/**
 * 历史页的内容：当前用户播放过的曲目，一首一行（标题与艺人、当前所在的专辑、最近一次播放的时间、播放次数、播放与“更多”），
 * 按最近一次播放倒序，没有排序控件。页码来自 URL；超出末页时显示空页并给出回到第一页的链接，不改写地址。
 * 文件缺失的曲目灰色显示并标“文件缺失”，专辑名不做链接（专辑可能已不可见），不能播放但能移除；文件回来后由数据自然恢复。
 * 移除与清除都先确认；成功后焦点回到区块标题（打开对话框的控件可能已经不在了），并用状态提示说明结果。
 */
export function HistoryView({ page, renderPlayAll, renderTrackActions, renderRowsScope }: HistoryViewProps) {
  const history = useHistoryTracks(page);
  const remove = useRemoveHistoryTrack();
  const clear = useClearPlayHistory();
  const [removing, setRemoving] = useState<HistoryItemDTO | null>(null);
  const [clearing, setClearing] = useState(false);
  const [outcome, setOutcome] = useState<Outcome>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusHeadingOnClose = useRef(false);
  const dialogOpen = removing !== null || clearing;

  useEffect(() => {
    // 对话框的清理先把焦点还给打开它的控件；成功后那个控件（被移除的行、清除按钮）可能已经消失，这里再把焦点带到标题。
    if (!dialogOpen && focusHeadingOnClose.current) {
      focusHeadingOnClose.current = false;
      headingRef.current?.focus();
    }
  }, [dialogOpen]);

  // 翻页的链接在表格下方：把结果顶部滚进视野并聚焦标题，接着 Tab 就是第一行（与专辑列表相同）。
  const showResultsTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };

  const columns = useMemo(
    (): Column<HistoryItemDTO>[] => [
      {
        id: "track",
        header: "曲目",
        rowHeader: true,
        cell: (item) => (
          <Muted item={item}>
            <span className={styles.title}>{historyTitleOf(item)}</span>
            <span className={styles.artist}>{historyArtistOf(item)}</span>
            {item.present ? null : (
              <span className={styles.tag}>
                <StatusTag tone="outline">文件缺失</StatusTag>
              </span>
            )}
          </Muted>
        ),
      },
      {
        id: "album",
        header: "专辑",
        cell: (item) => (
          <Muted item={item}>
            {item.present ? (
              <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: item.release.id }}>
                {item.release.title}
              </Link>
            ) : (
              item.release.title
            )}
          </Muted>
        ),
      },
      {
        id: "played-at",
        header: "最近播放",
        cell: (item) => (
          <Muted item={item}>
            <PlayedAt value={item.last_played_at} />
          </Muted>
        ),
      },
      {
        id: "count",
        header: "播放次数",
        numeric: true,
        cell: (item) => <Muted item={item}>{formatCount(item.play_count)}</Muted>,
      },
      {
        id: "actions",
        header: "操作",
        hideLabelWhenStacked: true,
        cell: (item) => renderTrackActions(item, () => setRemoving(item)),
      },
    ],
    [renderTrackActions],
  );

  const table = (items: readonly HistoryItemDTO[]) => <DataTable caption="播放历史" columns={columns} rows={items} rowKey={(item) => item.track_id} />;
  const hasHistory = history.data?.has_history === true;
  const closeRemove = () => setRemoving(null);
  const closeClear = () => setClearing(false);

  return (
    <>
      <Panel
        title="播放历史"
        headingRef={headingRef}
        action={
          hasHistory ? (
            <Button onClick={() => setClearing(true)}>清除全部历史</Button>
          ) : undefined
        }
      >
        {outcome?.kind === "cleared" && !hasHistory ? <Notice tone="success">已清除你的播放历史。</Notice> : null}
        {outcome?.kind === "removed" && outcome.page === page ? <Notice tone="success">已从历史中移除《{outcome.title}》。</Notice> : null}
        <QueryContent query={history} loading={<Skeleton label="正在加载播放历史" lines={4} />}>
          {(data) => {
            if (data.pagination.total === 0) {
              return (
                <p>
                  {PLAY_COUNT_RULE_TEXT} <Link to="/albums">去专辑页挑一张来听</Link>
                </p>
              );
            }
            const pageCount = pageCountOf(data.pagination.total, data.pagination.page_size);
            return (
              <div className={styles.results} aria-busy={history.isPlaceholderData}>
                {renderPlayAll()}
                {data.items.length === 0 ? (
                  <p>
                    这一页没有记录。{" "}
                    <Link to="/me/history" search={{}}>
                      回到第一页
                    </Link>
                  </p>
                ) : (
                  <>
                    {renderRowsScope === undefined ? table(data.items) : renderRowsScope(data.items, table(data.items))}
                    <p className={styles.note}>
                      共 {formatCount(data.pagination.total)} 首曲目
                    </p>
                    <Pagination
                      label="播放历史分页"
                      page={data.pagination.page}
                      pageCount={pageCount}
                      renderLink={(target, props) => (
                        <Link to="/me/history" search={{ page: target > 1 ? target : undefined }} onClick={showResultsTop} {...props} />
                      )}
                    />
                  </>
                )}
              </div>
            );
          }}
        </QueryContent>
      </Panel>
      <RemoveTrackDialog
        item={removing}
        state={remove.state}
        onConfirm={() => {
          if (removing === null) return;
          const title = historyTitleOf(removing);
          remove.remove(removing.track_id, {
            onSucceeded: () => {
              setOutcome({ kind: "removed", title, page });
              focusHeadingOnClose.current = true;
              closeRemove();
            },
          });
        }}
        onCancel={() => {
          remove.dismiss();
          closeRemove();
        }}
      />
      <ClearHistoryDialog
        open={clearing}
        state={clear.state}
        onConfirm={() =>
          clear.clear({
            onSucceeded: () => {
              setOutcome({ kind: "cleared" });
              focusHeadingOnClose.current = true;
              closeClear();
            },
          })
        }
        onCancel={() => {
          clear.dismiss();
          closeClear();
        }}
      />
    </>
  );
}
