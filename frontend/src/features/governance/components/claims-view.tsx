import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useEffect, useRef } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Notice, Pagination, QueryContent, Skeleton } from "../../../shared/ui";
import type { ClaimsSearch } from "../claims-search";
import { useClaim, useClaims } from "../hooks/use-claims";
import { useContainerWide } from "../hooks/use-container-wide";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import { ClaimDetail } from "./claim-detail";
import { ClaimList } from "./claim-list";
import { undoIntent } from "./release-admin-actions";
import { ReleaseCommandNotice } from "./release-command-notice";
import styles from "./claims-view.module.css";

/** 两栏的最小容器宽度：列表 380px 加详情的合理宽度；更窄时改为顺序导航（先列表、后详情）。 */
const TWO_COLUMN_MIN_WIDTH = 900;

type ClaimsViewProps = {
  search: ClaimsSearch;
  /** 认领与撤销的命令：由 app 创建（刷新什么由 app 决定）。 */
  commands: ReleaseCommands;
};

/**
 * 一个待认领的旧版本：读取、加载与失败态；成功后交给详情组件。换版本时按 key 重新挂载。
 * 版本已不需要认领（404：它的文件回来了，或已经认领过）时就地说明，不给无用的“重试”。
 */
function ClaimDetailPane({ releaseId, targetId, commands }: { releaseId: string; targetId: string | undefined; commands: ReleaseCommands }) {
  const claim = useClaim(releaseId);
  const navigate = useNavigate();
  if (claim.status === "error" && isHttpFailure(failureOf(claim.error), 404, "not_found")) {
    return <Notice tone="warning">这个版本现在不需要认领：它的文件回来了，或者它的修改已经认领给了别的版本。</Notice>;
  }
  return (
    <QueryContent query={claim} loading={<Skeleton label="正在加载这个版本留着的修改" lines={6} />}>
      {(data) => (
        <ClaimDetail
          detail={data}
          targetId={targetId}
          // 选择是同一个页面里的状态变化，不为每次点选留一个后退项：替换历史项。
          onSelectTarget={(target) => void navigate({ to: "/claims", search: (previous) => ({ ...previous, target }), replace: true })}
          commands={commands}
        />
      )}
    </QueryContent>
  );
}

/**
 * “待认领”页的内容（管理员）：扫描因为证明不了是同一个版本而新建了发行，旧发行不可见，它上面的修改被留在原处；这里按最后一次被看到的时间倒序列出它们。
 * 选中的版本写进地址（release）。容器足够宽时两栏（列表在左，选中的版本的详情在右）；窄于约 900px 时顺序导航：
 * 地址有 release 只显示详情与“返回待认领列表”，没有只显示列表。页码越界时用替换历史项的方式改到末页。
 */
export function ClaimsView({ search, commands }: ClaimsViewProps) {
  const page = search.page ?? 1;
  const noticeRef = useRef<HTMLDivElement>(null);
  // 认领成功之后提交按钮随预览一起被换掉（旧版本不再待认领），焦点不能掉到 body：落在结果提示上。
  const claimed = commands.state.status === "succeeded" && commands.state.command.kind === "claim" ? commands.state.command.key : null;
  useEffect(() => {
    if (claimed !== null) noticeRef.current?.focus();
  }, [claimed]);
  const claims = useClaims(page);
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const wide = useContainerWide(containerRef, TWO_COLUMN_MIN_WIDTH);
  const pagination = claims.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !claims.isPlaceholderData,
    onOutOfRange: (lastPage) => void navigate({ to: "/claims", search: (previous) => ({ ...previous, page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  const list = (
    <section className={styles.listColumn} aria-labelledby="claims-list-title">
      <h2 id="claims-list-title" className={styles.listTitle}>
        已不可见、留有修改的版本
      </h2>
      <QueryContent query={claims} loading={<Skeleton label="正在加载待认领的版本" lines={6} />}>
        {(data) =>
          outOfRange ? (
            <Skeleton label="正在加载待认领的版本" lines={6} />
          ) : data.pagination.total === 0 ? (
            <p className={styles.empty}>没有待认领的版本。扫描新建了发行、而旧发行上留有你做过的修改时，会出现在这里。</p>
          ) : (
            <div aria-busy={claims.isPlaceholderData} className={styles.listBody}>
              <ClaimList items={data.items} selectedId={search.release} />
              <p className={styles.note}>共 {formatCount(data.pagination.total)} 个版本，最近看到的在前。</p>
              <Pagination
                label="待认领列表分页"
                page={data.pagination.page}
                pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                renderLink={(target, props) => <Link to="/claims" search={(previous) => ({ ...previous, page: target > 1 ? target : undefined })} {...props} />}
              />
            </div>
          )
        }
      </QueryContent>
    </section>
  );

  const detail =
    search.release === undefined ? (
      <p className={styles.placeholder}>从列表里选一个版本，查看它留着的修改和可能接替它的版本。</p>
    ) : (
      <ClaimDetailPane key={search.release} releaseId={search.release} targetId={search.target} commands={commands} />
    );
  // 结果提示就近放在详情上方。认领之后旧版本从待认领里消失，详情随之变成“不需要认领”的说明，所以提示不属于哪个版本的详情：
  // 不限定版本（releaseId 为 null）。成功时带“撤销”和“打开这张专辑”。
  const notice = (
    <ReleaseCommandNotice
      state={commands.state}
      releaseId={null}
      hideFailure={false}
      successFocusRef={noticeRef}
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
          <Link to="/claims" search={(previous) => ({ ...previous, release: undefined, target: undefined })} className={styles.back}>
            <ArrowLeft size={16} aria-hidden="true" /> 返回待认领列表
          </Link>
          {notice}
          {detail}
        </div>
      )}
    </div>
  );
}
