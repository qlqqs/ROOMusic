import { ArtistCandidatesView, useArtistCommands } from "../../features/governance";
import { Page } from "../../shared/ui";
import { useArtistDataRefresh } from "./use-artist-refresh";

/** “可能是同一位”（管理员，路由层 AdminGate 把关）：合并或忽略之后刷新艺人、目录、搜索与收藏。page 来自地址。 */
export function ArtistCandidatesPage({ page }: { page: number }) {
  const refresh = useArtistDataRefresh();
  const commands = useArtistCommands({ refresh });
  return (
    <Page title="可能是同一位">
      <ArtistCandidatesView page={page} commands={commands} />
    </Page>
  );
}
