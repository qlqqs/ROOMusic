import { useNavigate } from "@tanstack/react-router";
import { Disc3, ListEnd, ListPlus, ListStart, MoreHorizontal, Play, UserRound } from "lucide-react";
import { useId, useState } from "react";
import { Button, MenuButton, VisuallyHidden, type MenuAction } from "../../shared/ui";
import { useMediaQuery } from "../../shared/lib/use-media-query";
import { useArtistLinksReady } from "../../features/catalog";
import { AddToPlaylistDialog, FavoriteButton, useFavoriteMenuAction, type FavoriteTarget } from "../../features/personal";
import { usePlaybackCapabilities, usePlayerEngine, type CapabilityTable, type PlayerController, type PlayerTrack } from "../../features/player";
import { unplayableReason } from "./track-playback";
import styles from "./track-actions.module.css";

// 曲目行的统一动作：行内播放、心形与“更多操作”（下一首播放、加入队列、添加到歌单…；前往艺人、前往专辑；再加页面自己的项）。
// 专辑、历史、收藏、歌单（以后还有搜索结果与艺人页）都用它，由 app 组合：player 与 personal 互不导入。
// 心形需要放在页面的 FavoriteStatusScope 里（一页一次批量读取收藏状态）。

export const QUEUE_FULL_TEXT = "队列已满，请先移除一些条目。";

type TrackActionsProps = {
  track: PlayerTrack;
  /** 文件是否还在；缺失的曲目不能播放、不能入队，但可以收藏、加入歌单与页面自己的操作（移除、排序）。 */
  present: boolean;
  /** 播放图标的读屏名和悬停提示（“播放《…》”“从这首开始播放”）。 */
  playLabel?: string;
  /** 行内播放：默认只放这一首、替换当前队列；专辑页从这首起放整张。返回要显示的提示（没有就是空字符串）。 */
  onPlay?: ((context: { engine: PlayerController; table: CapabilityTable }) => string) | undefined;
  /** 能力检查失败时，告诉用户去哪里重试（例如“请在专辑头部重试”）。 */
  retryHint: string;
  /** 页面自己的菜单项，放在通用项之后（从历史中移除、歌单的移动与移除）。 */
  extraActions?: readonly MenuAction[] | undefined;
  /**
   * 播放按钮放在哪里：inline（默认）与心形、“更多”一排；lead 是专辑曲目行的曲目号位置（按所在行给出的 --track-lead-* 定位，
   * 平时显示曲目号，悬停、聚焦或触屏时换成播放按钮，见 release-media.module.css）。
   */
  playPlacement?: "inline" | "lead";
  /** “更多”里有没有“前往专辑”：专辑页自己的曲目行不要（就在这张专辑里）。 */
  albumLink?: boolean;
};

const noActions: readonly MenuAction[] = [];

/** 窄屏（与外壳的断点相同）：曲目行只留“更多”，心形收进菜单。 */
const NARROW = "(max-width: 767px)";

/** 窄屏的“更多操作”：最前面多一项“收藏”或“取消收藏”。单独成组件：宽屏不为每一行多订阅一份收藏切换。 */
function NarrowTrackMenu({ favorite, report, actions }: { favorite: FavoriteTarget; report: (text: string) => void; actions: readonly MenuAction[] }) {
  const { action: favoriteAction, retry } = useFavoriteMenuAction(favorite, report);
  return <>
    <MenuButton label="更多操作" iconOnly icon={<MoreHorizontal size={16} />} actions={[favoriteAction, ...actions]} />
    {retry}
  </>;
}

/**
 * 一首曲目的行内动作：播放、心形与“更多操作”三个只有图标的按钮，一行不换行；窄屏上心形收进“更多”（“收藏”“取消收藏”）。
 * 播放按钮带 data-track-play：专辑曲目行双击时按它（等同于点它）。不能播放（文件缺失、格式或浏览器不支持）时播放、下一首与入队不可用，原因经 aria-describedby 可读；
 * 文件缺失已由行里的“文件缺失”标签显示，这里只给读屏说明。结果提示就近显示在这一行。
 */
export function TrackActions({ track, present, playLabel = "播放", onPlay, retryHint, extraActions = noActions, playPlacement = "inline", albumLink = true }: TrackActionsProps) {
  const engine = usePlayerEngine();
  const navigate = useNavigate();
  // 艺人数据就绪才有艺人页可去；多位艺人时去第一位。
  const artistLinked = useArtistLinksReady();
  const artist = artistLinked ? track.artistRefs?.[0] : undefined;
  const capability = usePlaybackCapabilities();
  const [notice, setNotice] = useState("");
  const [adding, setAdding] = useState(false);
  const reasonId = useId();
  const narrow = useMediaQuery(NARROW);
  const favoriteTarget: FavoriteTarget = { kind: "track", id: track.trackId, title: track.title };
  const reason = present && capability.isError ? `无法检查播放能力，${retryHint}` : unplayableReason(track, present, capability.data);
  function play() {
    const table = capability.data;
    if (table === undefined) return;
    if (onPlay !== undefined) {
      setNotice(onPlay({ engine, table }));
      return;
    }
    setNotice(engine.playTracks([track]) === "queue_full" ? QUEUE_FULL_TEXT : "");
  }
  function add(kind: "next" | "enqueue") {
    const result = kind === "next" ? engine.playNext([track]) : engine.enqueue([track]);
    setNotice(result === "queue_full" ? QUEUE_FULL_TEXT : kind === "next" ? "已设为下一首播放。" : "已加入队列。");
  }
  // 图标与专辑页头的“更多”同一套；跳转类的项单独一组（菜单项是按钮，在 onSelect 里跳转，与用户菜单的“设置”相同）。
  const actions: MenuAction[] = [
    { id: "next", label: "下一首播放", icon: <ListStart size={16} />, disabled: reason !== null, onSelect: () => add("next") },
    { id: "enqueue", label: "加入队列", icon: <ListEnd size={16} />, disabled: reason !== null, onSelect: () => add("enqueue") },
    {
      id: "playlist",
      label: "添加到歌单…",
      icon: <ListPlus size={16} />,
      onSelect: () => {
        setNotice("");
        setAdding(true);
      },
    },
    ...(artist === undefined
      ? []
      : [{ id: "artist", group: "go", untitledGroup: true, label: "前往艺人", hint: artist.name, icon: <UserRound size={16} />, onSelect: () => void navigate({ to: "/artists/$artistId", params: { artistId: artist.artist_id } }) }]),
    ...(albumLink
      ? [
          {
            id: "album",
            group: "go",
            untitledGroup: true,
            label: "前往专辑",
            hint: track.albumTitle,
            icon: <Disc3 size={16} />,
            onSelect: () => void navigate({ to: "/albums/$groupId", params: { groupId: track.groupId }, search: { release: track.releaseId, track: track.trackId } }),
          },
        ]
      : []),
    ...extraActions,
  ];
  return (
    <div className={styles.row}>
      <div className={styles.actions}>
        <span className={playPlacement === "lead" ? styles.lead : styles.inline}>
          <Button variant="icon" icon={<Play size={16} />} label={playLabel} disabled={reason !== null} aria-describedby={reason === null ? undefined : reasonId} onClick={play} data-track-play="" />
        </span>
        {narrow ? (
          <NarrowTrackMenu favorite={favoriteTarget} report={setNotice} actions={actions} />
        ) : (
          <>
            <FavoriteButton target={favoriteTarget} message={setNotice} />
            <MenuButton label="更多操作" iconOnly icon={<MoreHorizontal size={16} />} actions={actions} />
          </>
        )}
      </div>
      {reason === null ? null : present ? <p id={reasonId}>{reason}</p> : <VisuallyHidden><span id={reasonId}>{reason}</span></VisuallyHidden>}
      <p role="status">{notice}</p>
      {/* 只在打开时挂载：一张专辑可能有上万行，不为每一行预先放一个对话框。 */}
      {adding ? (
        <AddToPlaylistDialog
          open
          trackIds={[track.trackId]}
          subject={`《${track.title}》`}
          onClose={() => setAdding(false)}
          onAdded={(message) => {
            setAdding(false);
            setNotice(message);
          }}
        />
      ) : null}
    </div>
  );
}
