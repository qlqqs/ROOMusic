import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { useCallback, useEffect, useEffectEvent, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { useViewerScope } from "../../auth";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { isUuid } from "../../../shared/lib/ids";
import { Button, FailureNotice, ModalDialog, Notice, Page, Panel, QueryContent, Skeleton, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseDetailDTO, ReleaseGroupDetailDTO, ReleaseSummaryDTO, ReleaseTrackDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { albumArtistOf } from "../format";
import { useAlbum } from "../hooks/use-album";
import { useRelease } from "../hooks/use-release";
import { catalogKeys } from "../query-keys";
import { sharedTrackCredits } from "../track-credits";
import styles from "./album-detail-view.module.css";
import { AlbumHeader } from "./album-header";
import { EvidenceSection } from "./evidence-section";
import { ReleaseFacts } from "./release-facts";
import { ReleaseMedia, type TrackLocate } from "./release-media";
import { TrackInfo, type TrackPlace } from "./track-info";
import { VersionNav } from "./version-nav";

/** 标题超过这个长度（字符数）时降一级字号，完整显示并换行。 */
const LONG_TITLE_CHARS = 80;

/**
 * 头部操作区的上下文：选中版本的详情、它现在所在的组、组里可见的版本数（只有一个版本时没有默认版本与“移出为独立专辑”）、
 * 专辑的人工修改版本号（设定与清除默认版本的预期版本）与生效中的默认版本（自动选择时为 null）。
 * admin 只在 isAdmin 时为真：页面据此加入管理员的项（后端对每个写请求仍独立鉴权）。
 */
export type ReleaseActionsContext = { release: ReleaseDetailDTO; groupId: string; releaseCount: number; groupRevision: number; defaultReleaseId: string | null; admin: boolean };

type PlaybackSlots = {
  /** 头部的操作区（一条操作行与它下方的提示）；版本详情到达之后才渲染。传入稳定的函数引用。 */
  renderReleaseActions?: ((context: ReleaseActionsContext) => ReactNode) | undefined;
  /** 每行的动作；传入稳定的函数引用，曲目行才能跳过没有变化的重绘。showInfo 选中这一行并把焦点移到“曲目信息”（菜单里的“曲目信息”用它）。 */
  renderTrackActions?: ((track: ReleaseTrackDTO, release: ReleaseDetailDTO, showInfo: () => void) => ReactNode) | undefined;
  /** “曲目信息”里需要别的模块的项（响度），渲染成 <dt>/<dd>；传入稳定的函数引用。 */
  renderTrackFacts?: ((trackId: string) => ReactNode) | undefined;
  /** 播放器的当前曲目与它的状态文字（“正在播放”“已暂停”……）；catalog 只按它显示标记，不判断播放状态。 */
  currentTrackId?: string | undefined;
  currentTrackLabel?: string | undefined;
  /** 当前曲目正在播放（音柱跳动）；由播放器给出。 */
  currentTrackPlaying?: boolean | undefined;
  /**
   * 包住选中版本的头部与曲目区的范围（例如一次读出整个版本的收藏状态，供头部与每一行使用）：版本详情还没读到时 release 是 undefined。
   * catalog 不知道范围里是什么，只负责把内容交给它；传入稳定的函数引用。
   */
  renderReleaseScope?: ((context: { groupId: string; release: ReleaseDetailDTO | undefined }, children: ReactNode) => ReactNode) | undefined;
};

type AdminSlots = {
  /** 管理员的页面区块（修改记录），放在“来源与证据”之后；折叠与读取由区块自己负责。同样由页面按角色传入，这里再用 isAdmin 把一道。 */
  renderAdminSections?: ((context: { release: ReleaseDetailDTO; groupId: string }) => ReactNode) | undefined;
};

type AlbumDetailViewProps = PlaybackSlots & AdminSlots & {
  groupId: string;
  /** 地址里的 release 参数（路由已校验为 UUID 并转成小写）；没有表示代表版本。 */
  release: string | undefined;
  /** 地址里的 track 参数：要定位的曲目（路由已校验为 UUID 并转成小写）；没有就不定位。 */
  track?: string | undefined;
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
    <Page title="专辑不存在" actions={<BackToList list={list} />}>
      <p>它可能已被移除，或者地址不正确。</p>
    </Page>
  );
}

/** 一次定位请求与它完成后的回报：曲目区处理完（找到并定位，或这个版本里没有它）就调用 onLocated。 */
type LocateSlot = { locate?: TrackLocate | undefined; onLocated?: ((token: string) => void) | undefined };

type ReleaseBodyProps = PlaybackSlots & AdminSlots & LocateSlot & {
  groupId: string;
  group: ReleaseGroupDetailDTO;
  selected: ReleaseSummaryDTO;
  list: AlbumSearch;
  isAdmin: boolean;
};

/** 右栏排到曲目下方的宽度（与 .columns 的容器查询相同）：这时曲目信息改为从底部弹出的面板。 */
const SIDE_BY_SIDE_MIN = 720;

/** 版面是否单栏（右栏在曲目下方）：量 .layout 的宽度；没有 ResizeObserver（jsdom）时按并排处理。 */
function useStacked(element: RefObject<HTMLElement | null>): boolean {
  const [stacked, setStacked] = useState(false);
  useEffect(() => {
    const node = element.current;
    if (node === null || typeof ResizeObserver === "undefined") return;
    const measure = () => setStacked(node.getBoundingClientRect().width < SIDE_BY_SIDE_MIN);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [element]);
  return stacked;
}

/** 选中曲目与它在版本里的位置；版本里没有它（换了版本）时是 null。 */
function findTrack(release: ReleaseDetailDTO, trackId: string | null): { track: ReleaseTrackDTO; place: TrackPlace } | null {
  if (trackId === null) return null;
  for (const [mediumIndex, medium] of release.media.entries()) {
    const index = medium.tracks.findIndex((track) => track.id === trackId);
    const track = medium.tracks[index];
    if (track !== undefined) return { track, place: { disc: release.media.length > 1 ? mediumIndex + 1 : null, number: track.position >= 1 ? track.position : index + 1 } };
  }
  return null;
}

/**
 * 选中的曲目：单击一行选中、再次单击同一行或 Esc 取消；菜单里的“曲目信息”选中并把焦点移到曲目信息的标题。
 * Esc 在页面上监听：焦点在菜单或对话框里时由它们自己处理。
 */
function useTrackSelection() {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusPending = useRef(false);
  const toggle = useCallback((trackId: string) => setSelectedId((current) => (current === trackId ? null : trackId)), []);
  const show = useCallback((trackId: string) => {
    focusPending.current = true;
    setSelectedId(trackId);
  }, []);
  const clear = useCallback(() => setSelectedId(null), []);
  useEffect(() => {
    if (selectedId === null || !focusPending.current) return;
    focusPending.current = false;
    headingRef.current?.focus();
  }, [selectedId]);
  useEffect(() => {
    if (selectedId === null) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (event.target instanceof Element && event.target.closest("dialog, [role='menu']") !== null) return;
      setSelectedId(null);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [selectedId]);
  return { selectedId, headingRef, toggle, show, clear };
}

function ReleaseContent({ groupId, release, versioned, isAdmin, renderAdminSections, renderTrackActions, renderTrackFacts, currentTrackId, currentTrackLabel, currentTrackPlaying, locate, onLocated }: PlaybackSlots & Pick<AdminSlots, "renderAdminSections"> & LocateSlot & { groupId: string; release: ReleaseDetailDTO; versioned: boolean; isAdmin: boolean }) {
  const selection = useTrackSelection();
  const { show } = selection;
  // 版本与插槽不变时保持同一个函数，曲目行才能跳过重绘。
  const renderRowActions = useMemo(() => (renderTrackActions === undefined ? undefined : (track: ReleaseTrackDTO) => renderTrackActions(track, release, () => show(track.id))), [renderTrackActions, release, show]);
  // 全部曲目共有的署名已经在页头列过一次，曲目信息只写与它不同的部分。
  const sharedCredits = useMemo(() => sharedTrackCredits(release.media).keys, [release.media]);
  const albumArtist = albumArtistOf(release);
  const layoutRef = useRef<HTMLDivElement>(null);
  const stacked = useStacked(layoutRef);
  const sheetTitleId = useId();
  const selected = findTrack(release, selection.selectedId);
  const info = selected === null ? null : <TrackInfo track={selected.track} place={selected.place} albumArtist={albumArtist} sharedCredits={sharedCredits} renderExtraFacts={renderTrackFacts} headingRef={selection.headingRef} onClose={selection.clear} />;
  return (
    <>
      <div className={styles.layout} ref={layoutRef}>
        <div className={styles.columns}>
          <Panel title="曲目">
            <ReleaseMedia media={release.media} albumArtist={albumArtist} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} currentTrackPlaying={currentTrackPlaying} selectedTrackId={selected?.track.id} onSelectTrack={selection.toggle} renderTrackActions={renderRowActions} locate={locate} onLocated={onLocated} />
          </Panel>
          <aside className={styles.aside} aria-label="发行信息">
            <ReleaseFacts release={release} versioned={versioned} />
            {stacked ? null : info}
          </aside>
        </div>
      </div>
      {stacked ? (
        <ModalDialog open={info !== null} titleId={sheetTitleId} onRequestClose={selection.clear} closeOnBackdrop initialFocus={selection.headingRef} className={styles.sheet}>
          <div className={styles.sheetBody}>
            <VisuallyHidden>
              <span id={sheetTitleId}>曲目信息</span>
            </VisuallyHidden>
            {info}
          </div>
        </ModalDialog>
      ) : null}
      <EvidenceSection key={`evidence-${release.id}`} release={release} isAdmin={isAdmin} />
      {isAdmin ? renderAdminSections?.({ release, groupId }) : null}
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
      title="这个版本已经找不到了"
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
      可能刚重新扫描过。{isRepresentative ? "刷新后会显示这张专辑现在的内容。" : "可以回到这张专辑的代表版本。"}
    </Notice>
  );
}

/**
 * 选中版本的整页：页头（经 Page 的 header 插槽，由 AlbumHeader 渲染 h1）、版本导航、曲目、发行信息与证据。
 * 头部用组详情里的摘要先渲染，操作区与曲目区等版本详情；组详情刷新失败但还有旧数据时，错误显示在页头下方。
 */
function ReleaseBody({ groupId, group, groupFailure, selected, list, isAdmin, renderReleaseActions, renderAdminSections, renderTrackActions, renderTrackFacts, renderReleaseScope, currentTrackId, currentTrackLabel, currentTrackPlaying, locate, onLocated }: ReleaseBodyProps & { groupFailure: ReactNode }) {
  const detail = useRelease(selected.id);
  const gone = detail.isError && isHttpFailure(failureOf(detail.error), 404);
  const actions =
    detail.data === undefined
      ? undefined
      : renderReleaseActions?.({ release: detail.data, groupId, releaseCount: group.release_count, groupRevision: group.revision, defaultReleaseId: group.default_release_id, admin: isAdmin });
  const content = (
    <Page
      title={selected.title}
      header={<AlbumHeader release={selected} detail={detail.data} list={list} compactTitle={selected.title.length > LONG_TITLE_CHARS} actions={actions} />}
    >
      {groupFailure}
      {group.release_count > 1 ? (
        <VersionNav groupId={groupId} releases={group.releases} releaseCount={group.release_count} representativeId={group.representative.id} defaultReleaseId={group.default_release_id} list={list} />
      ) : null}
      {gone ? (
        <ReleaseGone groupId={groupId} group={group} selected={selected} />
      ) : (
        <QueryContent query={detail} loading={<Skeleton label="正在加载曲目" lines={6} />}>
          {(data) => <ReleaseContent groupId={groupId} release={data} versioned={group.release_count > 1} isAdmin={isAdmin} renderAdminSections={renderAdminSections} renderTrackActions={renderTrackActions} renderTrackFacts={renderTrackFacts} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} currentTrackPlaying={currentTrackPlaying} locate={locate} onLocated={onLocated} />}
        </QueryContent>
      )}
    </Page>
  );
  return renderReleaseScope === undefined ? content : renderReleaseScope({ groupId, release: detail.data }, content);
}

/** 地址里的 release 规范成最短形式：只有选中非代表版本时才保留，不在本组成员里的值丢弃。 */
function canonicalRelease(group: ReleaseGroupDetailDTO, requested: string | undefined): string | undefined {
  const member = requested === undefined ? undefined : group.releases.find((release) => release.id === requested);
  return member === undefined || member.id === group.representative.id ? undefined : member.id;
}

function AlbumDetailContent({ groupId, release: requested, track, list, isAdmin, renderReleaseActions, renderAdminSections, renderTrackActions, renderTrackFacts, renderReleaseScope, currentTrackId, currentTrackLabel, currentTrackPlaying }: AlbumDetailViewProps) {
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
    // 要定位的曲目随之保留：规范掉的只是版本参数。
    void navigate({ to: "/albums/$groupId", params: { groupId }, search: { release: canonical, track }, state: { albumList: list }, replace: true }),
  );
  useEffect(() => {
    if (needsNormalizing) normalize();
  }, [needsNormalizing, canonical]);

  // 定位曲目：每来到一条历史记录（打开、刷新、后退、前进，以及上面规范地址的替换）只定位一次，用历史记录的 key 区分；
  // 之后的重新渲染、轮询刷新不再滚动或抢焦点。地址还要规范化时先不定位，免得替换之后再定位一次。
  // 打开全屏播放器压入的记录带着原记录的 key（playerFrom），不算新的到达。
  const arrival = useRouterState({ select: (state) => state.location.state.playerFrom ?? state.location.state.__TSR_key ?? state.location.state.key ?? state.location.href });
  const [handled, setHandled] = useState<string | null>(null);
  const locate = useMemo(
    (): TrackLocate | undefined => (track === undefined || needsNormalizing || handled === arrival ? undefined : { trackId: track, token: arrival }),
    [track, needsNormalizing, handled, arrival],
  );
  const onLocated = useCallback((token: string) => setHandled(token), []);

  if (group.status === "error" && isHttpFailure(failureOf(group.error), 404)) return <AlbumNotFound list={list} />;

  const selected = data === undefined ? undefined : (data.releases.find((release) => release.id === canonical) ?? data.representative);
  if (data === undefined || selected === undefined) {
    // 组详情还没读到（或第一次读取就失败）：默认页头，标题是“专辑”。
    return (
      <Page title="专辑" actions={<BackToList list={list} />}>
        <QueryContent query={group} loading={<Skeleton label="正在加载专辑" lines={6} />}>
          {() => null}
        </QueryContent>
      </Page>
    );
  }
  const groupFailure = group.status === "error" ? <FailureNotice error={group.error} action={<Button onClick={() => void group.refetch()}>重试</Button>} /> : null;
  return <ReleaseBody groupId={groupId} group={data} groupFailure={groupFailure} selected={selected} list={list} isAdmin={isAdmin} renderReleaseActions={renderReleaseActions} renderAdminSections={renderAdminSections} renderTrackActions={renderTrackActions} renderTrackFacts={renderTrackFacts} renderReleaseScope={renderReleaseScope} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} currentTrackPlaying={currentTrackPlaying} locate={locate} onLocated={onLocated} />;
}

/**
 * 专辑详情：一个组的头部、版本导航（多于一个版本时）、介质与曲目、发行信息与来源证据。
 * 组 ID 不是 UUID 时不发请求，直接显示“专辑不存在”；服务器返回 404 同样如此。
 */
export function AlbumDetailView(props: AlbumDetailViewProps) {
  return isUuid(props.groupId) ? <AlbumDetailContent {...props} /> : <AlbumNotFound list={props.list} />;
}
