import type { ReactNode } from "react";
import { TriangleAlert } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseSummaryDTO } from "../api/decoders";
import { formatDuration } from "../../../shared/lib/duration";
import { formatGenres, isFieldCorrected, totalDuration } from "../format";
import styles from "./album-header.module.css";
import { Cover } from "./cover";
import { ManualCorrectionTag } from "./manual-correction-tag";

type AlbumHeaderProps = {
  actions?: ReactNode;
  /** 管理员的操作区（编辑发行信息、调整归组与上一次操作的结果）；由页面按角色传入，普通用户没有。 */
  adminActions?: ReactNode;
  /** 选中版本的摘要（来自组详情）：先于曲目到达，所以封面与计数不必等详情。 */
  release: ReleaseSummaryDTO;
  /** 版本详情，到达前为 undefined：流派与总时长要等它。 */
  detail: ReleaseDetailDTO | undefined;
};

/** field 是这项概况对应的字段名，有人工修正时标出“人工修正”。 */
type Fact = { label: string; value: string; field?: string };

function factsOf(release: ReleaseSummaryDTO, detail: ReleaseDetailDTO | undefined): Fact[] {
  const genre = detail === undefined ? "" : formatGenres(detail.genres);
  // 有一首曲目的时长未知就不写总时长：不拿已知的部分冒充总数。
  const duration = detail === undefined ? null : totalDuration(detail.media);
  return [
    { label: "年份", value: release.year === null ? "未知" : String(release.year), field: "year" },
    ...(genre === "" ? [] : [{ label: "流派", value: genre, field: "genre" }]),
    { label: "介质", value: `${formatCount(release.medium_count)} 张` },
    { label: "曲目", value: `${formatCount(release.track_count)} 首` },
    ...(duration === null ? [] : [{ label: "总时长", value: formatDuration(duration) }]),
  ];
}

/**
 * 详情页的头部：封面与版本概况。标题与专辑艺人是页面的 h1 与说明，由 Page 给出。
 * “需要关注”只是提示核对，这里不把它写成错误，说明在底部的“来源与证据”里。
 */
export function AlbumHeader({ release, detail, actions, adminActions }: AlbumHeaderProps) {
  const corrected = detail?.corrected_fields ?? [];
  return (
    <div className={styles.container}>
      <div className={styles.hero}>
        <div className={styles.cover}>
          <Cover artwork={release.artwork} title={release.title} variant="detail" />
        </div>
        <div className={styles.info}>
          {actions}
          {adminActions}
          <dl className={styles.facts}>
            {factsOf(release, detail).map((fact) => (
              <div key={fact.label} className={styles.fact}>
                <dt>{fact.label}</dt>
                <dd>{fact.value}</dd>
                {fact.field !== undefined && isFieldCorrected(corrected, fact.field) ? (
                  <dd className={styles.factTag}>
                    <ManualCorrectionTag />
                  </dd>
                ) : null}
              </div>
            ))}
          </dl>
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
    </div>
  );
}
