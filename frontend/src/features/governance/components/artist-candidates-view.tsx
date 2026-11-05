import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, FailureNotice, Pagination, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { artistMetaText, artistRoleLabels, type ArtistSummaryDTO } from "../../catalog";
import { artistsKeys, fetchArtist } from "../../artists";
import type { ArtistCandidateDTO } from "../api/decoders";
import type { ArtistCommand, ArtistCommandResult, ArtistCommands } from "../hooks/use-artist-commands";
import { useArtistCandidates } from "../hooks/use-artist-candidates";
import { ArtistCommandNotice } from "./artist-command-notice";
import { MergeCandidateDialog } from "./merge-artist-dialog";
import styles from "./artist-candidates-view.module.css";

/** 一条理由：说清楚看到了什么，不下“就是同一个人”的结论。 */
function reasonText(reason: ArtistCandidateDTO["reasons"][number]): string {
  const role = reason.role === undefined || reason.role === null ? "" : artistRoleLabels[reason.role];
  if (reason.kind === "same_track_role") return `同一首曲目的${role === "" ? "署名" : role}写了“${reason.left_name}”和“${reason.right_name}”`;
  return `同一张专辑的专辑艺人写“${reason.left_name}”，曲目艺人写“${reason.right_name}”`;
}

function ArtistLink({ artist }: { artist: ArtistSummaryDTO }) {
  const meta = artistMetaText(artist);
  return (
    <span className={styles.artist}>
      <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={styles.name}>
        {artist.name}
      </Link>
      {meta === "" ? null : <span className={styles.meta}>{meta}</span>}
    </span>
  );
}

/** 待处理（默认）或已忽略；已忽略只在地址里写 status=ignored。 */
export type ArtistCandidateStatus = "pending" | "ignored";

/**
 * 条目的 key：同一对艺人超过 100 个名字对时会分成几条，只用两位艺人的 ID 会重复，所以带上第一个名字对。
 * 同时用来标出正在读版本号的那一条。
 */
const candidateKey = (candidate: ArtistCandidateDTO) => {
  const first = candidate.key_pairs[0];
  return `${candidate.left.id}:${candidate.right.id}:${first?.left_key_id ?? ""}:${first?.right_key_id ?? ""}`;
};

type CandidateItemProps = {
  candidate: ArtistCandidateDTO;
  status: ArtistCandidateStatus;
  busy: boolean;
  onMerge: () => void;
  onIgnore: () => void;
  onRestore: () => void;
};

function CandidateItem({ candidate, status, busy, onMerge, onIgnore, onRestore }: CandidateItemProps) {
  const label = `“${candidate.left.name}”和“${candidate.right.name}”`;
  return (
    <li className={styles.item}>
      <p className={styles.pair}>
        <ArtistLink artist={candidate.left} />
        <span className={styles.and}>和</span>
        <ArtistLink artist={candidate.right} />
      </p>
      <ul className={styles.reasons}>
        {candidate.reasons.map((reason, index) => (
          <li key={index}>{reasonText(reason)}</li>
        ))}
      </ul>
      {candidate.evidence_count > candidate.reasons.length ? <p className={styles.meta}>共 {formatCount(candidate.evidence_count)} 处</p> : null}
      <div className={styles.actions}>
        {status === "ignored" ? (
          <Button busy={busy} onClick={onRestore} aria-label={`恢复${label}的提示`}>
            恢复提示
          </Button>
        ) : (
          <>
            <Button onClick={onMerge} aria-label={`合并${label}…`}>
              合并…
            </Button>
            <Button variant="text" busy={busy} onClick={onIgnore} aria-label={`忽略${label}`}>
              忽略
            </Button>
          </>
        )}
      </div>
    </li>
  );
}

/** 待处理 / 已忽略的切换：链接不带页码，切换后回到第一页；不显示数目（当前列表的总数在 Panel 标题右侧）。 */
function CandidateStatusFilter({ status }: { status: ArtistCandidateStatus }) {
  return (
    <nav aria-label="候选状态" className={styles.filterRow}>
      {(["pending", "ignored"] as const).map((target) => (
        <Link
          key={target}
          to="/artist-candidates"
          search={{ status: target === "ignored" ? "ignored" : undefined }}
          // Link 激活时自己设 aria-current；默认的部分匹配会忽略值为 undefined 的 status，让“待处理”在已忽略视图里也算激活。
          // explicitUndefined 让 status: undefined 参与比较：只有地址里没有 status 时“待处理”才是当前项，page 不影响。
          activeOptions={{ explicitUndefined: true }}
          aria-current={status === target ? "page" : undefined}
          className={styles.filter}
        >
          {target === "ignored" ? "已忽略" : "待处理"}
        </Link>
      ))}
    </nav>
  );
}

/**
 * “可能是同一位”的候选（管理员）：只是提示，理由来自明确的标签信号。可以合并（选留下哪一位）或忽略；忽略后同一对名字不再提示，
 * 结果提示里可以马上恢复，之后也能在“已忽略”里逐条恢复（已忽略的不提供合并，先恢复再到待处理里操作）。
 * 合并、忽略、恢复都要两边的版本号，在点下去时读两位艺人的详情。状态与页码在地址里，越界时改到末页。
 */
export function ArtistCandidatesView({ page, status, commands }: { page: number; status: ArtistCandidateStatus; commands: ArtistCommands }) {
  const ignoredView = status === "ignored";
  const statusSearch = ignoredView ? ("ignored" as const) : undefined;
  const candidates = useArtistCandidates(page, ignoredView);
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [merging, setMerging] = useState<ArtistCandidateDTO | null>(null);
  const [loading, setLoading] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const pagination = candidates.data?.pagination;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !candidates.isPlaceholderData,
    onOutOfRange: (lastPage) => void navigate({ to: "/artist-candidates", search: { status: statusSearch, page: lastPage > 1 ? lastPage : undefined }, replace: true }),
  });

  // 忽略与恢复需要两边现在的版本号：读两位艺人的详情（不沿用旧缓存），再提交。
  const revisionOf = async (artistId: string) => {
    const response = await queryClient.fetchQuery({ queryKey: [...artistsKeys.all(generation), "revision", artistId], queryFn: ({ signal }) => fetchArtist(artistId, signal), staleTime: 0, gcTime: 0 });
    return response.data.revision;
  };
  const ignore = async (candidate: ArtistCandidateDTO, ignored: boolean) => {
    setLoading(candidateKey(candidate));
    setLoadError(null);
    commands.dismiss();
    try {
      const [revision, otherRevision] = await Promise.all([revisionOf(candidate.left.id), revisionOf(candidate.right.id)]);
      commands.submit({
        kind: "ignore",
        artist: { id: candidate.left.id, name: candidate.left.name },
        revision,
        other: { id: candidate.right.id, name: candidate.right.name },
        otherRevision,
        keyPairs: candidate.key_pairs,
        ignored,
      });
    } catch (error) {
      setLoadError(error);
    } finally {
      setLoading(null);
    }
  };
  // 刚忽略的提示马上恢复：版本号用这次结果里的。
  const unignore = (command: ArtistCommand, result: ArtistCommandResult) => {
    if (command.kind !== "ignore" || result.kind !== "ignore") return;
    commands.submit({ ...command, revision: result.result.revision, otherRevision: result.result.other_revision, ignored: false });
  };

  if (candidates.isError && candidates.data === undefined && isHttpFailure(failureOf(candidates.error), 503, "artist_catalog_preparing")) {
    return <p role="status">艺人信息正在整理，请稍后再试。</p>;
  }
  const pendingIgnore = commands.state.status === "pending" && commands.state.command.kind === "ignore" ? commands.state.command : null;
  return (
    <>
      <CandidateStatusFilter status={status} />
      <ArtistCommandNotice state={commands.state} artistId={null} hideFailure={merging !== null} onUnignore={unignore} onRetry={() => commands.retry()} onDismiss={commands.dismiss} />
      {loadError === null ? null : <FailureNotice error={loadError} action={<Button onClick={() => setLoadError(null)}>关闭</Button>} />}
      <Panel title={ignoredView ? "已忽略" : "候选"} headingRef={headingRef} action={pagination === undefined ? undefined : <span>{formatCount(pagination.total)} 条</span>}>
        <QueryContent query={candidates} loading={<Skeleton label="正在加载候选" lines={4} />}>
          {(data) =>
            outOfRange ? (
              <Skeleton label="正在加载候选" lines={4} />
            ) : data.pagination.total === 0 ? (
              <p>{ignoredView ? "没有忽略过的候选。" : "现在没有需要确认的候选。"}</p>
            ) : (
              <div className={styles.results} aria-busy={candidates.isPlaceholderData}>
                <ul className={styles.list}>
                  {data.items.map((candidate) => {
                    const key = candidateKey(candidate);
                    const busy = loading === key || (pendingIgnore !== null && pendingIgnore.artist.id === candidate.left.id && pendingIgnore.other.id === candidate.right.id);
                    return (
                      <CandidateItem
                        key={key}
                        candidate={candidate}
                        status={status}
                        busy={busy}
                        onMerge={() => {
                          commands.dismiss();
                          setMerging(candidate);
                        }}
                        onIgnore={() => void ignore(candidate, true)}
                        onRestore={() => void ignore(candidate, false)}
                      />
                    );
                  })}
                </ul>
                <Pagination
                  label="候选分页"
                  page={data.pagination.page}
                  pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                  renderLink={(target, props) => (
                    <Link
                      to="/artist-candidates"
                      search={{ status: statusSearch, page: target > 1 ? target : undefined }}
                      onClick={() => {
                        headingRef.current?.scrollIntoView({ block: "start" });
                        headingRef.current?.focus({ preventScroll: true });
                      }}
                      {...props}
                    />
                  )}
                />
              </div>
            )
          }
        </QueryContent>
      </Panel>
      <MergeCandidateDialog pair={merging} commands={commands} onClose={() => setMerging(null)} />
    </>
  );
}
