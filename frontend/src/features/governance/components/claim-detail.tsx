import { Link } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { DataTable, type Column } from "../../../shared/ui";
import type { ClaimDetailDTO } from "../api/decoders";
import { intentText, locationText } from "../claim-text";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import { correctionFieldLabels, displayValue, reviewableIssueLabels } from "../labels";
import { ClaimPreview } from "./claim-preview";
import { ClaimTargetPicker } from "./claim-target-picker";
import styles from "./claim-detail.module.css";

type Correction = ClaimDetailDTO["corrections"][number];
type Review = ClaimDetailDTO["reviews"][number];

function CorrectionsSection({ corrections }: { corrections: readonly Correction[] }) {
  const columns: readonly Column<Correction>[] = [
    { id: "field", header: "字段", rowHeader: true, cell: (item) => correctionFieldLabels[item.field] },
    { id: "correction", header: "你改成了", cell: (item) => intentText(item.correction) },
    { id: "scan", header: "扫描值", cell: (item) => displayValue(item.scan_value) },
  ];
  return (
    <section className={styles.section} aria-labelledby="claim-corrections">
      <h3 id="claim-corrections" tabIndex={-1}>
        字段修正（{formatCount(corrections.length)} 项）
      </h3>
      <DataTable caption="旧版本上的字段修正" columns={columns} rows={corrections} rowKey={(item) => item.field} />
    </section>
  );
}

function AlbumSection({ detail }: { detail: ClaimDetailDTO }) {
  if (detail.album === null && detail.default_of.length === 0) return null;
  return (
    <section className={styles.section} aria-labelledby="claim-album">
      <h3 id="claim-album" tabIndex={-1}>
        所在的专辑
      </h3>
      {detail.album === null ? null : (
        <>
          <p className={styles.explain}>它和另外 {formatCount(detail.album.other_release_count)} 个版本同在《{detail.album.title}》这张专辑里。</p>
          {detail.siblings.length === 0 ? null : (
            <ul className={styles.bullets}>
              {detail.siblings.map((sibling) => (
                <li key={sibling.id}>
                  {sibling.visible ? (
                    <Link to="/albums/$groupId" params={{ groupId: detail.album?.id ?? "" }} search={{ release: sibling.id }}>
                      {sibling.title}
                    </Link>
                  ) : (
                    <>
                      {sibling.title} <span className={styles.hidden}>（已不可见）</span>
                    </>
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {detail.default_of.map((album) => (
        <p key={album.id} className={styles.explain}>
          它是《{album.title}》这张专辑的默认版本。
        </p>
      ))}
    </section>
  );
}

function ReviewsSection({ reviews }: { reviews: readonly Review[] }) {
  const columns: readonly Column<Review>[] = [
    { id: "issue", header: "问题", rowHeader: true, cell: (item) => reviewableIssueLabels[item.issue] },
    { id: "reviewer", header: "核对人", cell: (item) => (item.reviewer.username === "" ? "（用户已不存在）" : item.reviewer.username) },
    { id: "time", header: "核对时间", cell: (item) => formatDateTime(item.reviewed_at) },
  ];
  return (
    <section className={styles.section} aria-labelledby="claim-reviews">
      <h3 id="claim-reviews" tabIndex={-1}>
        问题核对（{formatCount(reviews.length)} 项）
      </h3>
      <DataTable caption="旧版本上的问题核对" columns={columns} rows={reviews} rowKey={(item) => item.issue} />
    </section>
  );
}

type ClaimDetailProps = {
  detail: ClaimDetailDTO;
  /** 选中的新版本（地址里的 target）。 */
  targetId: string | undefined;
  onSelectTarget: (releaseId: string) => void;
  commands: ReleaseCommands;
};

/**
 * 一个待认领的旧版本：头部是它的名字（有修正时是修正后的，管理员认得出）、位置与最后一次看到的时间；然后分节列出它留着的修改
 * （字段修正、所在的专辑与默认版本、问题核对，没有的节不出现）；最后是选择接替它的新版本（推荐与搜索）和认领的预览。
 * 各节的标题可以被程序聚焦（tabIndex=-1，不进 Tab 顺序）。
 */
export function ClaimDetail({ detail, targetId, onSelectTarget, commands }: ClaimDetailProps) {
  const pending = commands.state.status === "pending" && commands.state.command.kind === "claim";
  const { release } = detail;
  return (
    <article className={styles.detail} aria-labelledby="claim-detail-title">
      <header className={styles.header}>
        <h2 id="claim-detail-title" className={styles.title}>
          {release.title}
        </h2>
        <p className={styles.meta}>{[release.artist, release.edition].filter((part) => part !== null).join(" · ")}</p>
        <p className={styles.meta}>
          已不可见：扫描没有在原来的位置找到它的音乐文件。最后在 {locationText(detail.location)}，{formatDateTime(detail.last_seen_at)} 最后一次看到。
        </p>
      </header>
      <p className={styles.explain}>
        扫描认为这是另一个版本，新建了一个发行，你在这个旧版本上做过的修改被留在原处，看不到效果，也改不了。下面是它留着的修改。
      </p>
      {detail.corrections.length === 0 ? null : <CorrectionsSection corrections={detail.corrections} />}
      <AlbumSection detail={detail} />
      {detail.reviews.length === 0 ? null : <ReviewsSection reviews={detail.reviews} />}
      <section className={styles.section} aria-labelledby="claim-target">
        <h3 id="claim-target" tabIndex={-1}>
          选择接替它的版本
        </h3>
        <p className={styles.explain}>选一个现在可见的版本，旧版本上的修改会迁给它。新版本上已经有的修改以新版本为准，下面的预览逐项说明。</p>
        <fieldset className={styles.pickerLock} disabled={pending}>
          <ClaimTargetPicker suggestions={detail.suggestions} selectedId={targetId} onSelect={onSelectTarget} />
        </fieldset>
        <ClaimPreview detail={detail} targetId={targetId} commands={commands} />
      </section>
    </article>
  );
}
