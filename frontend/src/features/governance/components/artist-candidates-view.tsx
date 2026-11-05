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

type CandidateItemProps = {
  candidate: ArtistCandidateDTO;
  busy: boolean;
  onMerge: () => void;
  onIgnore: () => void;
};

function CandidateItem({ candidate, busy, onMerge, onIgnore }: CandidateItemProps) {
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
        <Button onClick={onMerge} aria-label={`合并${label}…`}>
          合并…
        </Button>
        <Button variant="text" busy={busy} onClick={onIgnore} aria-label={`忽略${label}`}>
          忽略
        </Button>
      </div>
    </li>
  );
}

/**
 * “可能是同一位”的候选（管理员）：只是提示，理由来自明确的标签信号。可以合并（选留下哪一位）或忽略；忽略后同一对名字不再提示，
 * 结果提示里可以马上恢复。合并、忽略都要两边的版本号，在点下去时读两位艺人的详情。页码在地址里，越界时改到末页。
 */
export function ArtistCandidatesView({ page, commands }: { page: number; commands: ArtistCommands }) {
  const candidates = useArtistCandidates(page);
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
    onOutOfRange: (lastPage) => void navigate({ to: "/artist-candidates", search: { page: lastPage > 1 ? lastPage : undefined }, replace: true }),
  });

  // 忽略需要两边现在的版本号：读两位艺人的详情（不沿用旧缓存），再提交。
  const revisionOf = async (artistId: string) => {
    const response = await queryClient.fetchQuery({ queryKey: [...artistsKeys.all(generation), "revision", artistId], queryFn: ({ signal }) => fetchArtist(artistId, signal), staleTime: 0, gcTime: 0 });
    return response.data.revision;
  };
  const ignore = async (candidate: ArtistCandidateDTO, ignored: boolean) => {
    const id = `${candidate.left.id}:${candidate.right.id}`;
    setLoading(id);
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
      <ArtistCommandNotice state={commands.state} artistId={null} hideFailure={merging !== null} onUnignore={unignore} onRetry={() => commands.retry()} onDismiss={commands.dismiss} />
      {loadError === null ? null : <FailureNotice error={loadError} action={<Button onClick={() => setLoadError(null)}>关闭</Button>} />}
      <Panel title="候选" headingRef={headingRef} action={pagination === undefined ? undefined : <span>{formatCount(pagination.total)} 条</span>}>
        <QueryContent query={candidates} loading={<Skeleton label="正在加载候选" lines={4} />}>
          {(data) =>
            outOfRange ? (
              <Skeleton label="正在加载候选" lines={4} />
            ) : data.pagination.total === 0 ? (
              <p>现在没有需要确认的候选。</p>
            ) : (
              <div className={styles.results} aria-busy={candidates.isPlaceholderData}>
                <ul className={styles.list}>
                  {data.items.map((candidate) => {
                    const id = `${candidate.left.id}:${candidate.right.id}`;
                    const busy = loading === id || (pendingIgnore !== null && pendingIgnore.artist.id === candidate.left.id && pendingIgnore.other.id === candidate.right.id);
                    return (
                      <CandidateItem
                        key={id}
                        candidate={candidate}
                        busy={busy}
                        onMerge={() => {
                          commands.dismiss();
                          setMerging(candidate);
                        }}
                        onIgnore={() => void ignore(candidate, true)}
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
                      search={{ page: target > 1 ? target : undefined }}
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
