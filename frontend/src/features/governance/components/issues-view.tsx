import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useRef } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Notice, Pagination, QueryContent, Skeleton } from "../../../shared/ui";
import { useContainerWide } from "../hooks/use-container-wide";
import { useIssues, useReleaseIssues } from "../hooks/use-issues";
import { issueCategoryLabels } from "../issue-text";
import { ISSUE_CATEGORIES, type IssueCategory, type IssuesSearch } from "../issues-search";
import { IssueList } from "./issue-list";
import type { IssuePageDTO } from "../api/decoders";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import { FieldIssueActions, ReviewAction } from "./issue-actions";
import { undoIntent } from "./release-admin-actions";
import { ReleaseCommandNotice } from "./release-command-notice";
import { ReleaseIssuesDetail } from "./release-issues-detail";
import styles from "./issues-view.module.css";

/** 两栏的最小容器宽度：列表 380px 加详情的合理宽度；更窄时改为顺序导航（先列表、后详情）。 */
const TWO_COLUMN_MIN_WIDTH = 900;

type IssuesViewProps = {
  search: IssuesSearch;
  /** 核对、取消核对、编辑发行信息与撤销的命令：由 app 创建（刷新什么由 app 决定）。 */
  commands: ReleaseCommands;
};

/** 筛选项上的数字（看得见的只是数字；可访问名称由链接的 aria-label 写全，如“待处理，2 个版本”）。 */
function FilterCount({ count }: { count: string }) {
  return <span className={styles.count}>{count}</span>;
}

/** 筛选链接的可访问名称：名称后面接数目；数目还没读到时只有名称。 */
const filterName = (name: string, count: string | null) => (count === null ? name : `${name}，${count} 个版本`);

/** 筛选：状态（待处理、已核对）与类别，都是链接（写进地址），当前项带 aria-current；数字来自 summary，不随类别与分页变化。 */
function IssueFilters({ search, summary }: { search: IssuesSearch; summary: IssuePageDTO["summary"] | undefined }) {
  const status = search.status ?? "open";
  const counts = summary?.[status];
  const countOf = (category: IssueCategory | undefined) => {
    if (counts === undefined) return null;
    return formatCount(category === undefined ? counts.all : counts[category]);
  };
  return (
    <div className={styles.filters}>
      <nav aria-label="问题状态" className={styles.filterRow}>
        {(["open", "reviewed"] as const).map((target) => (
          <Link
            key={target}
            to="/issues"
            search={{ status: target === "reviewed" ? "reviewed" : undefined, category: search.category }}
            aria-current={status === target ? "page" : undefined}
            aria-label={filterName(target === "open" ? "待处理" : "已核对", summary === undefined ? null : formatCount(summary[target].all))}
            className={styles.filter}
          >
            {target === "open" ? "待处理" : "已核对"}
            {summary === undefined ? null : <FilterCount count={formatCount(summary[target].all)} />}
          </Link>
        ))}
      </nav>
      <nav aria-label="问题类别" className={styles.filterRow}>
        {[undefined, ...ISSUE_CATEGORIES].map((category) => (
          <Link
            key={category ?? "all"}
            to="/issues"
            search={{ status: search.status, category }}
            aria-current={search.category === category ? "page" : undefined}
            aria-label={filterName(category === undefined ? "全部" : issueCategoryLabels[category], countOf(category))}
            className={styles.filter}
          >
            {category === undefined ? "全部" : issueCategoryLabels[category]}
            {countOf(category) === null ? null : <FilterCount count={countOf(category) as string} />}
          </Link>
        ))}
      </nav>
    </div>
  );
}

/**
 * 一个版本的问题：读取、加载与失败态；成功后交给详情组件，各节带上核对与编辑的操作。换版本时按 key 重新挂载。
 * 版本已不可见（404）时就地说明，不给无用的“重试”。
 */
function IssueDetailPane({ releaseId, commands }: { releaseId: string; commands: ReleaseCommands }) {
  const issues = useReleaseIssues(releaseId);
  if (issues.status === "error" && isHttpFailure(failureOf(issues.error), 404, "not_found")) {
    return <Notice tone="warning">这个版本已经找不到了，可能刚重新扫描过。</Notice>;
  }
  return (
    <QueryContent query={issues} loading={<Skeleton label="正在加载这个版本的问题" lines={6} />}>
      {(data) => {
        const release = { release_id: data.release_id, revision: data.revision, title: data.release.title };
        return (
          <ReleaseIssuesDetail
            issues={data}
            renderFieldActions={(fields) => <FieldIssueActions fields={fields} release={release} commands={commands} />}
            renderReviewAction={(subject) => <ReviewAction subject={subject} release={release} commands={commands} />}
          />
        );
      }}
    </QueryContent>
  );
}

/**
 * “元数据问题”页的内容（管理员）：按版本列出“需要关注”的问题，可以按状态与类别筛选；选中的版本写进地址（release）。
 * 容器足够宽时两栏（列表在左，选中的版本的问题在右）；窄于约 900px 时顺序导航：地址有 release 只显示详情与“返回问题列表”，没有只显示列表。
 * 处理完、离开当前筛选的版本仍保持选中，详情说明“已经没有待处理的问题”，页面不跳走。页码越界时用替换历史项的方式改到末页。
 */
export function IssuesView({ search, commands }: IssuesViewProps) {
  const status = search.status ?? "open";
  const page = search.page ?? 1;
  const issues = useIssues({ status, category: search.category, page });
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const wide = useContainerWide(containerRef, TWO_COLUMN_MIN_WIDTH);
  const pagination = issues.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !issues.isPlaceholderData,
    onOutOfRange: (lastPage) => void navigate({ from: "/issues", to: "/issues", search: (previous) => ({ ...previous, page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  const list = (
    <section className={styles.listColumn} aria-labelledby="issues-list-title">
      <h2 id="issues-list-title" className={styles.listTitle}>
        有问题的版本
      </h2>
      <IssueFilters search={search} summary={issues.data?.summary} />
      <QueryContent query={issues} loading={<Skeleton label="正在加载问题列表" lines={6} />}>
        {(data) =>
          outOfRange ? (
            <Skeleton label="正在加载问题列表" lines={6} />
          ) : data.pagination.total === 0 ? (
            <p className={styles.empty}>
              {status === "reviewed" ? "没有已核对的问题。" : search.category === undefined ? "没有待处理的问题。" : "这一类没有待处理的问题。"}
            </p>
          ) : (
            <div aria-busy={issues.isPlaceholderData} className={styles.listBody}>
              <IssueList items={data.items} status={status} selectedId={search.release} />
              <p className={styles.note}>共 {formatCount(data.pagination.total)} 个版本</p>
              <Pagination
                label="问题列表分页"
                page={data.pagination.page}
                pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                renderLink={(target, props) => <Link from="/issues" to="/issues" search={(previous) => ({ ...previous, page: target > 1 ? target : undefined })} {...props} />}
              />
            </div>
          )
        }
      </QueryContent>
    </section>
  );

  const detail =
    search.release === undefined ? (
      <p className={styles.placeholder}>从列表里选一个版本。</p>
    ) : (
      <IssueDetailPane key={search.release} releaseId={search.release} commands={commands} />
    );
  // 结果提示就近放在详情上方，只显示选中的版本的结果；成功时带“撤销”。
  const notice =
    search.release === undefined ? null : (
      <ReleaseCommandNotice
        state={commands.state}
        releaseId={search.release}
        hideFailure={false}
        onUndo={(command, result) => {
          const intent = undoIntent(command, result);
          if (intent !== null) commands.submit(intent);
        }}
        onRetry={() => commands.retry()}
        onDismiss={commands.dismiss}
      />
    );

  return (
    <div ref={containerRef} className={styles.view} data-layout={wide ? "columns" : "sequential"}>
      {wide ? (
        <>
          {list}
          <div className={styles.detailColumn}>
            {notice}
            {detail}
          </div>
        </>
      ) : search.release === undefined ? (
        list
      ) : (
        <div className={styles.detailColumn}>
          <Link from="/issues" to="/issues" search={(previous) => ({ ...previous, release: undefined })} className={styles.back}>
            <ArrowLeft size={16} aria-hidden="true" /> 返回问题列表
          </Link>
          {notice}
          {detail}
        </div>
      )}
    </div>
  );
}
