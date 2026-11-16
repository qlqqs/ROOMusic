// 页面上下文：随消息发给管家的“正在看”“正在播放”（C5 的 R5、R6）。形状与长度上限按后端的请求合同；
// 标题来自文件标签，是不可信文本，界面里只按文本节点渲染，后端按带来源标记的附加内容交给模型。

export const pageKinds = ["album", "artist", "playlist", "track", "other"] as const;
export type PageKind = (typeof pageKinds)[number];

/** 当前页面：路径与类型；对象页还有 ID，读到了标题时带上标题。 */
export type StewardPageContext = { path: string; kind: PageKind; id?: string; title?: string };

/** 当前控制的分区与正在放的曲目；分区里没有曲目时只有分区与状态。 */
export type StewardPlaybackContext = {
  zone_id: string;
  zone_name?: string;
  track_id?: string;
  title?: string;
  artist?: string;
  status: string;
};

/** 发消息与继续的请求体里的 context；两项都被点掉或都没有时不发这个字段。 */
export type StewardRequestContext = { page?: StewardPageContext; playback?: StewardPlaybackContext };

/** 草稿里点掉的标签：记下点掉时那一项的标识，换了页面或曲目后新的标签照常出现。 */
export type DismissedContext = { page: string | null; playback: string | null };

export const noDismissed: DismissedContext = { page: null, playback: null };

// 后端的上限（按字符计）：路径 512，标题类 300。超出的截断，不让整条消息因为一个过长的标签被拒绝。
const MAX_PATH = 512;
const MAX_TEXT = 300;

function clip(value: string, max: number): string {
  const chars = Array.from(value);
  return chars.length <= max ? value : chars.slice(0, max).join("");
}

export function pageIdentityOf(page: StewardPageContext): string {
  return page.path;
}

export function playbackIdentityOf(playback: StewardPlaybackContext): string {
  return `${playback.zone_id}\n${playback.track_id ?? ""}`;
}

/** 输入框上方要显示的标签（没被点掉的）；都没有时是 null。 */
export function visibleContextOf(page: StewardPageContext | null, playback: StewardPlaybackContext | null, dismissed: DismissedContext) {
  const shownPage = page !== null && dismissed.page !== pageIdentityOf(page) ? page : null;
  const shownPlayback = playback !== null && dismissed.playback !== playbackIdentityOf(playback) ? playback : null;
  return { page: shownPage, playback: shownPlayback };
}

/** 请求体里的 context：只含没被点掉的项，长度按后端上限截断；什么都没有时是 null。 */
export function requestContextOf(page: StewardPageContext | null, playback: StewardPlaybackContext | null, dismissed: DismissedContext): StewardRequestContext | null {
  const shown = visibleContextOf(page, playback, dismissed);
  const context: StewardRequestContext = {};
  if (shown.page !== null) {
    const { path, kind, id, title } = shown.page;
    context.page = { path: clip(path, MAX_PATH), kind, ...(id === undefined ? {} : { id }), ...(title === undefined ? {} : { title: clip(title, MAX_TEXT) }) };
  }
  if (shown.playback !== null) {
    const { zone_id, zone_name, track_id, title, artist, status } = shown.playback;
    context.playback = {
      zone_id,
      ...(zone_name === undefined ? {} : { zone_name: clip(zone_name, MAX_TEXT) }),
      ...(track_id === undefined ? {} : { track_id }),
      ...(title === undefined ? {} : { title: clip(title, MAX_TEXT) }),
      ...(artist === undefined ? {} : { artist: clip(artist, MAX_TEXT) }),
      status,
    };
  }
  return context.page === undefined && context.playback === undefined ? null : context;
}
