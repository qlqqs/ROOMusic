import { Link } from "@tanstack/react-router";
import { Columns2 } from "lucide-react";
import { useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Button } from "../../../shared/ui";
import type { ReleaseSummaryDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { DefaultVersionTag } from "./default-version-tag";
import { VersionComparisonDialog } from "./version-comparison-dialog";
import styles from "./version-nav.module.css";

type VersionNavProps = {
  groupId: string;
  /** 组的成员版本（最多 100 个），按代表版本规则排序。 */
  releases: readonly ReleaseSummaryDTO[];
  /** 组里全部可见版本的数量，可以大于 releases 的长度。 */
  releaseCount: number;
  /** 代表版本：默认选中，它的链接不带 release 参数。 */
  representativeId: string;
  /** 生效中的默认版本（管理员指定的，它就是第一个版本）；自动选择时为 null，没有任何版本带“默认版本”标签。 */
  defaultReleaseId: string | null;
  /** 从列表带进来的筛选与页码：切换版本时随链接保留，“返回专辑列表”仍然有效。 */
  list: AlbumSearch;
};

/**
 * 版本之间的区别：成员摘要里有的字段（年份、介质类型、介质与曲目数）。摘要不含版本说明与编号，
 * 那些要读详情才有，列表里不为每个版本各发一次请求。
 */
function versionDetail(release: ReleaseSummaryDTO): string {
  const parts: string[] = [];
  if (release.year !== null) parts.push(String(release.year));
  const kind = (release.media_type ?? release.source_type ?? "").trim();
  if (kind !== "") parts.push(kind);
  if (release.medium_count > 1) parts.push(`${formatCount(release.medium_count)} 张介质`);
  parts.push(`${formatCount(release.track_count)} 首曲目`);
  return parts.join(" · ");
}

/**
 * 多版本组的版本导航（调用方只在 release_count > 1 时渲染）。选择写进地址，所以用链接而不是 ARIA 标签页：
 * 前进、后退与分享都成立，也不需要方向键的焦点管理。当前版本的链接带 aria-current（路由按地址自动设置）。
 * 标题旁的“对比版本”（所有登录成员）打开对比对话框；生效中的默认版本带“默认版本”标签。
 */
export function VersionNav({ groupId, releases, releaseCount, representativeId, defaultReleaseId, list }: VersionNavProps) {
  const [comparing, setComparing] = useState(false);
  // 各版本的标题完全相同时标题不提供区分信息，不重复显示。
  const titlesDiffer = new Set(releases.map((release) => release.title)).size > 1;
  return (
    <nav aria-label="版本" className={styles.nav}>
      <div className={styles.top}>
        <h2 className={styles.heading}>版本</h2>
        <Button icon={<Columns2 size={16} />} onClick={() => setComparing(true)}>
          对比版本
        </Button>
      </div>
      <ul className={styles.list}>
        {releases.map((release, index) => (
          <li key={release.id} className={styles.item}>
            <Link
              to="/albums/$groupId"
              params={{ groupId }}
              search={{ release: release.id === representativeId ? undefined : release.id }}
              state={{ albumList: list }}
              activeOptions={{ exact: true }}
              className={styles.link}
            >
              {/* 各段之间的空格让链接的可访问名称读起来是“版本 2 2015 · CD · 12 首曲目”，而不是连成一串。 */}
              <span className={styles.name}>版本 {index + 1}</span>{" "}
              {release.id === defaultReleaseId ? (
                <>
                  <DefaultVersionTag />{" "}
                </>
              ) : null}
              {titlesDiffer ? (
                <>
                  <span className={styles.title}>{release.title}</span>{" "}
                </>
              ) : null}
              <span className={styles.detail}>{versionDetail(release)}</span>
            </Link>
          </li>
        ))}
      </ul>
      {releaseCount > releases.length ? (
        <p className={styles.note}>
          共 {formatCount(releaseCount)} 个版本，这里只列出前 {formatCount(releases.length)} 个。
        </p>
      ) : null}
      <VersionComparisonDialog open={comparing} groupId={groupId} list={list} onClose={() => setComparing(false)} />
    </nav>
  );
}
