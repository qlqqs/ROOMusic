import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useEffectEvent, useMemo, type ReactNode } from "react";
import { useViewerScope } from "../../auth";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { isUuid } from "../../../shared/lib/ids";
import { Button, Notice, Page, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseGroupDetailDTO, ReleaseSummaryDTO, ReleaseTrackDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { albumArtistOf } from "../format";
import { useAlbum } from "../hooks/use-album";
import { useRelease } from "../hooks/use-release";
import { catalogKeys } from "../query-keys";
import styles from "./album-detail-view.module.css";
import { AlbumHeader } from "./album-header";
import { EvidenceSection } from "./evidence-section";
import { ReleaseFacts } from "./release-facts";
import { ReleaseMedia } from "./release-media";
import { VersionNav } from "./version-nav";

/** 标题超过这个长度（字符数）时降一级字号，完整显示并换行。 */
const LONG_TITLE_CHARS = 80;

type PlaybackSlots = {
  renderReleaseActions?: ((release: ReleaseDetailDTO) => ReactNode) | undefined;
  /** 每行的动作；传入稳定的函数引用，曲目行才能跳过没有变化的重绘。 */
  renderTrackActions?: ((track: ReleaseTrackDTO, release: ReleaseDetailDTO) => ReactNode) | undefined;
  /** 播放器的当前曲目与它的状态文字（“正在播放”“已暂停”……）；catalog 只按它显示标记，不判断播放状态。 */
  currentTrackId?: string | undefined;
  currentTrackLabel?: string | undefined;
};

type AdminSlots = {
  /**
   * 管理员的操作区（编辑发行信息、调整归组与上一次操作的结果），放在头部。由页面按角色传入；
   * 这里再用 isAdmin 把一道，后端对每个写请求仍独立鉴权。context.releaseCount 是这个组里可见的版本数。
   */
  renderAdminActions?: ((context: { release: ReleaseDetailDTO; groupId: string; releaseCount: number }) => ReactNode) | undefined;
  /** 管理员的页面区块（修改记录），放在“来源与证据”之后；折叠与读取由区块自己负责。同样由页面按角色传入，这里再用 isAdmin 把一道。 */
  renderAdminSections?: ((context: { release: ReleaseDetailDTO }) => ReactNode) | undefined;
};

type AlbumDetailViewProps = PlaybackSlots & AdminSlots & {
  groupId: string;
  /** 地址里的 release 参数（路由已校验为 UUID 并转成小写）；没有表示代表版本。 */
  release: string | undefined;
  /** 进入详情之前的列表筛选与页码：“返回专辑列表”据此恢复。 */
  list: AlbumSearch;
  /** 管理员在“来源与证据”里多一个完整证据；后端仍独立鉴权。 */
  isAdmin: boolean;
};

function BackToList({ list }: { list: AlbumSearch }) {
  return (
    <Link to="/albums" search={list}>
      返回专辑列表
    </Link>
  );
}

function AlbumNotFound({ list }: { list: AlbumSearch }) {
  return (
    <Page title="专辑不存在" description="没有找到这张专辑。" actions={<BackToList list={list} />}>
      <p>它可能已被移除，或者地址不正确。</p>
    </Page>
  );
}

type ReleaseBodyProps = PlaybackSlots & AdminSlots & {
  groupId: string;
  group: ReleaseGroupDetailDTO;
  selected: ReleaseSummaryDTO;
  list: AlbumSearch;
  isAdmin: boolean;
};

function ReleaseContent({ release, versioned, isAdmin, renderAdminSections, renderTrackActions, currentTrackId, currentTrackLabel }: PlaybackSlots & Pick<AdminSlots, "renderAdminSections"> & { release: ReleaseDetailDTO; versioned: boolean; isAdmin: boolean }) {
  // 版本与插槽不变时保持同一个函数，曲目行才能跳过重绘。
  const renderRowActions = useMemo(() => (renderTrackActions === undefined ? undefined : (track: ReleaseTrackDTO) => renderTrackActions(track, release)), [renderTrackActions, release]);
  return (
    <>
      <div className={styles.layout}>
        <div className={styles.columns}>
          <Panel title="曲目">
            <ReleaseMedia media={release.media} albumArtist={albumArtistOf(release)} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} renderTrackActions={renderRowActions} />
          </Panel>
          <aside className={styles.aside} aria-label="发行信息">
            <ReleaseFacts release={release} versioned={versioned} />
          </aside>
        </div>
      </div>
      <EvidenceSection key={`evidence-${release.id}`} release={release} isAdmin={isAdmin} />
      {isAdmin ? renderAdminSections?.({ release }) : null}
    </>
  );
}

/**
 * 选中的版本在组详情里还是成员，读它的详情却是 404：组详情与版本详情是两次请求，中间可能重扫。
 * 就地说明，并给出办法：选中的不是代表版本就回到代表版本，是代表版本就刷新；操作时让组详情过期，版本导航随之刷新。
 */
function ReleaseGone({ groupId, group, selected }: { groupId: string; group: ReleaseGroupDetailDTO; selected: ReleaseSummaryDTO }) {
  const queryClient = useQueryClient();
  const { generation } = useViewerScope();
  // 组详情与这个版本的详情一起过期：重新读到的成员列表决定还有哪些版本，这个版本若又出现了也会重新读。
  const refreshGroup = () => {
    void queryClient.invalidateQueries({ queryKey: catalogKeys.group(generation, groupId) });
    void queryClient.invalidateQueries({ queryKey: catalogKeys.release(generation, selected.id) });
  };
  const isRepresentative = selected.id === group.representative.id;
  return (
    <Notice
      tone="warning"
      title="这个版本已不可见"
      action={
        isRepresentative ? (
          <Button onClick={refreshGroup}>刷新</Button>
        ) : (
          <Link to="/albums/$groupId" params={{ groupId }} search={{ release: undefined }} onClick={refreshGroup}>
            查看代表版本
          </Link>
        )
      }
    >
      可能刚被重新扫描。{isRepresentative ? "刷新后会显示这张专辑现在的内容。" : "可以回到这张专辑的代表版本。"}
    </Notice>
  );
}

/** 选中版本的头部、曲目、发行信息与证据。头部用组详情里的摘要先渲染，曲目区单独显示加载态。 */
function ReleaseBody({ groupId, group, selected, list, isAdmin, renderReleaseActions, renderAdminActions, renderAdminSections, renderTrackActions, currentTrackId, currentTrackLabel }: ReleaseBodyProps) {
  const detail = useRelease(selected.id);
  const gone = detail.isError && isHttpFailure(failureOf(detail.error), 404);
  return (
    <>
      <AlbumHeader
        release={selected}
        detail={detail.data}
        actions={detail.data === undefined ? undefined : renderReleaseActions?.(detail.data)}
        adminActions={detail.data === undefined || !isAdmin ? undefined : renderAdminActions?.({ release: detail.data, groupId, releaseCount: group.release_count })}
      />
      {group.release_count > 1 ? (
        <VersionNav groupId={groupId} releases={group.releases} releaseCount={group.release_count} representativeId={group.representative.id} defaultReleaseId={group.default_release_id} list={list} />
      ) : null}
      {gone ? (
        <ReleaseGone groupId={groupId} group={group} selected={selected} />
      ) : (
        <QueryContent query={detail} loading={<Skeleton label="正在加载曲目" lines={6} />}>
          {(data) => <ReleaseContent release={data} versioned={group.release_count > 1} isAdmin={isAdmin} renderAdminSections={renderAdminSections} renderTrackActions={renderTrackActions} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} />}
        </QueryContent>
      )}
    </>
  );
}

/** 地址里的 release 规范成最短形式：只有选中非代表版本时才保留，不在本组成员里的值丢弃。 */
function canonicalRelease(group: ReleaseGroupDetailDTO, requested: string | undefined): string | undefined {
  const member = requested === undefined ? undefined : group.releases.find((release) => release.id === requested);
  return member === undefined || member.id === group.representative.id ? undefined : member.id;
}

function AlbumDetailContent({ groupId, release: requested, list, isAdmin, renderReleaseActions, renderAdminActions, renderAdminSections, renderTrackActions, currentTrackId, currentTrackLabel }: AlbumDetailViewProps) {
  const group = useAlbum(groupId);
  const navigate = useNavigate();
  const data = group.data;
  const canonical = data === undefined ? undefined : canonicalRelease(data, requested);
  // 组详情正在刷新时不规范化：写操作（归组、默认版本）会先把地址带到要显示的版本、再刷新数据，缓存里的旧成员列表这时
  // 可能还不含这个版本，或还把旧的代表版本当作代表版本；按旧数据规范化会把 release 参数去掉，刷新完成后页面就停在了别的版本。
  // 等刷新结束，再按新数据规范化。
  const needsNormalizing = data !== undefined && !group.isFetching && canonical !== requested;
  const normalize = useEffectEvent(() =>
    // 替换历史项，返回键不会回到被规范掉的地址；列表参数随之保留，“返回专辑列表”仍然有效。
    void navigate({ to: "/albums/$groupId", params: { groupId }, search: { release: canonical }, state: { albumList: list }, replace: true }),
  );
  useEffect(() => {
    if (needsNormalizing) normalize();
  }, [needsNormalizing, canonical]);

  if (group.status === "error" && isHttpFailure(failureOf(group.error), 404)) return <AlbumNotFound list={list} />;

  const selected = data === undefined ? undefined : (data.releases.find((release) => release.id === canonical) ?? data.representative);
  return (
    <Page
      title={selected?.title ?? "专辑"}
      compactTitle={selected !== undefined && selected.title.length > LONG_TITLE_CHARS}
      {...(selected === undefined ? {} : { description: albumArtistOf(selected) })}
      actions={<BackToList list={list} />}
    >
      <QueryContent query={group} loading={<Skeleton label="正在加载专辑" lines={6} />}>
        {(loaded) =>
          selected === undefined ? null : <ReleaseBody groupId={groupId} group={loaded} selected={selected} list={list} isAdmin={isAdmin} renderReleaseActions={renderReleaseActions} renderAdminActions={renderAdminActions} renderAdminSections={renderAdminSections} renderTrackActions={renderTrackActions} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} />
        }
      </QueryContent>
    </Page>
  );
}

/**
 * 专辑详情：一个组的头部、版本导航（多于一个版本时）、介质与曲目、发行信息与来源证据。
 * 组 ID 不是 UUID 时不发请求，直接显示“专辑不存在”；服务器返回 404 同样如此。
 */
export function AlbumDetailView(props: AlbumDetailViewProps) {
  return isUuid(props.groupId) ? <AlbumDetailContent {...props} /> : <AlbumNotFound list={props.list} />;
}
