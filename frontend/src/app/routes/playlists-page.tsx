import { PlaylistsView } from "../../features/personal";
import { Page } from "../../shared/ui";

/** 我的歌单：列表与新建都在 personal；page 来自地址。 */
export function PlaylistsPage({ page }: { page: number }) {
  return (
    <Page title="我的歌单">
      <PlaylistsView page={page} />
    </Page>
  );
}
