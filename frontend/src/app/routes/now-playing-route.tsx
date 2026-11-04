import type { PlayerTrack } from "../../features/player";
import { NowPlayingPage } from "../../features/player";
import { CurrentTrackFavorite, SaveQueueAsPlaylist } from "../player-personal-actions";

// 模块级函数：插槽引用保持不变。
const renderCurrentTrackActions = (track: PlayerTrack) => <CurrentTrackFavorite track={track} />;
const renderQueueActions = (tracks: readonly PlayerTrack[]) => <SaveQueueAsPlaylist tracks={tracks} />;

/** 正在播放页：player 的页面，当前曲目的心形与队列的“存为歌单”由这里经插槽放进去。 */
export function NowPlayingRoute() {
  return <NowPlayingPage renderCurrentTrackActions={renderCurrentTrackActions} renderQueueActions={renderQueueActions} />;
}
