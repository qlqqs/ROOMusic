import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useRef } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Notice, Pagination, QueryContent, Skeleton } from "../../../shared/ui";
import type { ClaimsSearch } from "../claims-search";
import { useClaim, useClaims } from "../hooks/use-claims";
import { useContainerWide } from "../hooks/use-container-wide";
import { ClaimDetail } from "./claim-detail";
import { ClaimList } from "./claim-list";
import styles from "./claims-view.module.css";

/** 两栏的最小容器宽度：列表 380px 加详情的合理宽度；更窄时改为顺序导航（先列表、后详情）。 */
const TWO_COLUMN_MIN_WIDTH = 900;

type ClaimsViewProps = {
  search: ClaimsSearch;
};

/**
 * 一个待认领的旧版本：读取、加载与失败态；成功后交给详情组件。换版本时按 key 重新挂载。
 * 版本已不需要认领（404：它的文件回来了，或已经认领过）时就地说明，不给无用的“重试”。
 */
function ClaimDetailPane({ releaseId }: { releaseId: string }) {
  const claim = useClaim(releaseId);
  if (claim.status === "error" && isHttpFailure(failureOf(claim.error), 404, "not_found")) {
    return <Notice tone="warning">这个版本现在不需要认领：它的文件回来了，或者它的修改已经认领给了别的版本。</Notice>;
  }
  return (
    <QueryContent query={claim} loading={<Skeleton label="正在加载这个版本留着的修改" lines={6} />}>
      {(data) => <ClaimDetail detail={data} />}
    </QueryContent>
  );
}

/**
 * “待认领”页的内容（管理员）：扫描因为证明不了是同一个版本而新建了发行，旧发行不可见，它上面的修改被留在原处；这里按最后一次被看到的时间倒序列出它们。
 * 选中的版本写进地址（release）。容器足够宽时两栏（列表在左，选中的版本的详情在右）；窄于约 900px 时顺序导航：
 * 地址有 release 只显示详情与“返回待认领列表”，没有只显示列表。页码越界时用替换历史项的方式改到末页。
 */
export function ClaimsView({ search }: ClaimsViewProps) {
  const page = search.page ?? 1;
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
      <ClaimDetailPane key={search.release} releaseId={search.release} />
    );

  return (
    <div ref={containerRef} className={styles.view} data-layout={wide ? "columns" : "sequential"}>
      {wide ? (
        <>
          {list}
          <div className={styles.detailColumn}>{detail}</div>
        </>
      ) : search.release === undefined ? (
        list
      ) : (
        <div className={styles.detailColumn}>
          <Link to="/claims" search={(previous) => ({ ...previous, release: undefined })} className={styles.back}>
            <ArrowLeft size={16} aria-hidden="true" /> 返回待认领列表
          </Link>
          {detail}
        </div>
      )}
    </div>
  );
}
