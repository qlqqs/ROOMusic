import { PlaylistsView } from "../../features/personal";

/** “我的音乐”的歌单页签：列表与新建都在 personal；page 来自地址。 */
export function PlaylistsPage({ page }: { page: number }) {
  return <PlaylistsView page={page} />;
}
