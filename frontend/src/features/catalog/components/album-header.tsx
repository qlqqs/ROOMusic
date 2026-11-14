import { Link } from "@tanstack/react-router";
import { ChevronLeft, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseSummaryDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { albumArtistRefsOf } from "../api/track-summary";
import { formatSpokenDuration } from "../../../shared/lib/duration";
import { albumArtistOf, formatGenres, isFieldCorrected, totalDuration } from "../format";
import styles from "./album-header.module.css";
import { ArtistNames } from "./artist-names";
import { Cover } from "./cover";
import { ManualCorrectionTag } from "./manual-correction-tag";

type AlbumHeaderProps = {
  /** 选中版本的摘要（来自组详情）：先于曲目到达，所以封面、标题与计数不必等详情。 */
  release: ReleaseSummaryDTO;
  /** 版本详情，到达前为 undefined：流派与总时长要等它。 */
  detail: ReleaseDetailDTO | undefined;
  /** 进入详情之前的列表筛选与页码：“‹ 专辑”据此恢复；没有时回到不带筛选的专辑列表。 */
  list?: AlbumSearch;
  /** 标题很长时降一级字号，完整显示并换行。 */
  compactTitle?: boolean;
  /**
   * 概况下方的操作区（由页面组合：一条操作行，以及它下方的状态与结果提示）；详情到达之前没有。
   * catalog 不知道里面有什么，只负责位置。
   */
  actions?: ReactNode;
};

/** 概况只写值；label 只给读屏。field 是这项对应的字段名，有人工修正时标出“人工修正”。 */
type Fact = { label: string; value: string; field?: string };

/** 年份未知、单碟不写；有一首曲目的时长未知就不写总时长：不拿已知的部分冒充总数。 */
function factsOf(release: ReleaseSummaryDTO, detail: ReleaseDetailDTO | undefined): Fact[] {
  const genre = detail === undefined ? "" : formatGenres(detail.genres);
  const duration = detail === undefined ? null : totalDuration(detail.media);
  return [
    ...(release.year === null ? [] : [{ label: "年份", value: String(release.year), field: "year" }]),
    ...(genre === "" ? [] : [{ label: "流派", value: genre, field: "genre" }]),
    ...(release.medium_count > 1 ? [{ label: "介质", value: `${formatCount(release.medium_count)} 张碟` }] : []),
    { label: "曲目", value: `${formatCount(release.track_count)} 首` },
    ...(duration === null || duration <= 0 ? [] : [{ label: "总时长", value: formatSpokenDuration(duration) }]),
  ];
}

/**
 * 专辑详情的页头（经 Page 的 header 插槽替换默认页头，渲染唯一的 h1，文字与页面标题相同）：
 * 标题上方是“‹ 专辑”（读屏名“返回专辑列表”）；封面在左，右侧依次是专辑名、专辑艺人、一行概况与操作区，整体底对齐封面。
 * “需要关注”只是提示核对，这里不把它写成错误，说明在底部的“来源与证据”里。
 */
const noFilters: AlbumSearch = {};

export function AlbumHeader({ release, detail, list = noFilters, compactTitle = false, actions }: AlbumHeaderProps) {
  const corrected = detail?.corrected_fields ?? [];
  const artist = albumArtistOf(release);
  return (
    <header className={styles.container}>
      <Link to="/albums" search={list} className={styles.back} aria-label="返回专辑列表">
        <ChevronLeft size={16} aria-hidden="true" />
        专辑
      </Link>
      <div className={styles.hero}>
        <div className={styles.cover}>
          <Cover artwork={release.artwork} title={release.title} variant="detail" />
        </div>
        <div className={styles.info}>
          <div className={styles.heading} data-compact={compactTitle ? "" : undefined}>
            <h1>{release.title}</h1>
            {artist === "" ? null : (
              <p className={styles.artist}>
                <ArtistNames text={artist} refs={albumArtistRefsOf(release)} />
              </p>
            )}
          </div>
          <div className={styles.factsClip}>
            <ul className={styles.facts} aria-label="概况">
              {factsOf(release, detail).map((fact) => (
                <li key={fact.label}>
                  <VisuallyHidden>{fact.label}：</VisuallyHidden>
                  {fact.value}
                  {fact.field !== undefined && isFieldCorrected(corrected, fact.field) ? (
                    <span className={styles.factTag}>
                      <ManualCorrectionTag />
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          </div>
          {actions === undefined ? null : <div className={styles.actions}>{actions}</div>}
          {release.attention_count > 0 ? (
            <p className={styles.attention}>
              <StatusTag tone="outline" icon={<TriangleAlert size={12} />}>
                需要关注
              </StatusTag>{" "}
              <span>详见页面底部的“来源与证据”。</span>
            </p>
          ) : null}
        </div>
      </div>
    </header>
  );
}
