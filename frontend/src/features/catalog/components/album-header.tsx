import { Link } from "@tanstack/react-router";
import { ChevronLeft, TriangleAlert } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseSummaryDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { albumArtistRefsOf } from "../api/track-summary";
import { formatSpokenDuration } from "../../../shared/lib/duration";
import { albumArtistOf, fieldMarksOf, formatGenres, totalDuration } from "../format";
import styles from "./album-header.module.css";
import { ArtistNames } from "./artist-names";
import { Cover } from "./cover";
import { FieldMarkTag, hasFieldMark } from "./field-mark-tag";
import { sharedTrackCredits, type TrackCreditGroup } from "../track-credits";

type AlbumHeaderProps = {
  /** 选中版本的摘要（来自组详情）：先于曲目到达，所以封面、标题与计数不必等详情。 */
  release: ReleaseSummaryDTO;
  /** 版本详情，到达前为 undefined：流派与总时长要等它。 */
  detail: ReleaseDetailDTO | undefined;
  /** 进入详情之前的列表筛选与页码：“‹ 专辑”据此恢复；没有时回到不带筛选的专辑列表。 */
  list?: AlbumSearch;
  /** 标题很长时降一级字号，完整显示并换行。 */
  compactTitle?: boolean;
  /** 这张专辑正在播放：标题旁一个“正在播放”的小标记（跳动的音柱，减少动态效果时静止）。 */
  playing?: boolean;
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
 * 全部曲目共有的署名（“全部曲目 作曲 甲 钢琴 乙”）：页头列一次，曲目行不再重复。艺人就绪时名字是链接。
 * “全部曲目”在窄屏上只给读屏（居中的页头里一行更短）。
 */
function SharedCredits({ groups }: { groups: readonly TrackCreditGroup[] }) {
  return (
    // 组与组之间只靠留白分开：读屏另有看不见的标点（“全部曲目：作曲：甲；钢琴：乙”），不连成一串。
    <p className={styles.credits}>
      <span className={styles.creditsLead}>全部曲目</span>
      <VisuallyHidden>：</VisuallyHidden>
      {groups.map((group, groupIndex) => (
        <span key={group.key} className={styles.credit}>
          {groupIndex === 0 ? null : <VisuallyHidden>；</VisuallyHidden>}
          <span className={styles.creditLabel}>{group.label}</span>
          <VisuallyHidden>：</VisuallyHidden>{" "}
          {group.entries.map((credit, index) => (
            <span key={`${credit.name}:${index}`} className={styles.creditName}>
              {index === 0 ? null : "、"}
              <ArtistNames text={credit.name} refs={credit.artist_refs} />
            </span>
          ))}
        </span>
      ))}
    </p>
  );
}

/**
 * 专辑详情的页头（经 Page 的 header 插槽替换默认页头，渲染唯一的 h1，文字与页面标题相同）：
 * 标题上方是“‹ 专辑”（读屏名“返回专辑列表”）；封面在左，右侧依次是专辑名、专辑艺人、一行概况、全部曲目共有的署名与操作区，整体底对齐封面。
 * 容器窄（手机）时封面放大居中，文字与操作行一起居中。
 * “需要关注”只是提示核对，这里不把它写成错误，说明在底部的“来源与证据”里。
 */
const noFilters: AlbumSearch = {};

export function AlbumHeader({ release, detail, list = noFilters, compactTitle = false, playing = false, actions }: AlbumHeaderProps) {
  const marks = detail === undefined ? { corrected: [], auto: undefined } : fieldMarksOf(detail);
  const artist = albumArtistOf(release);
  const media = detail?.media;
  const shared = useMemo(() => (media === undefined ? [] : sharedTrackCredits(media).groups), [media]);
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
            <h1>
              {release.title}
              {/* 标记只给看得见的人：标题的可访问名称只是专辑名，播放状态由曲目行与播放栏说明。 */}
              {playing ? (
                <span className={styles.nowPlaying} aria-hidden="true">
                  <span className={styles.eq}>
                    <i />
                    <i />
                    <i />
                  </span>
                  正在播放
                </span>
              ) : null}
            </h1>
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
                  {fact.field !== undefined && hasFieldMark(marks, fact.field) ? (
                    <span className={styles.factTag}>
                      <FieldMarkTag marks={marks} field={fact.field} />
                    </span>
                  ) : null}
                </li>
              ))}
            </ul>
          </div>
          {shared.length === 0 ? null : <SharedCredits groups={shared} />}
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
