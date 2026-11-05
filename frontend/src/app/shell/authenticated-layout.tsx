import { Outlet } from "@tanstack/react-router";
import { useSession, ViewerScopeProvider } from "../../features/auth";
import { PersonalSync } from "../../features/personal";
import { PlayerProvider } from "../../features/player";
import { PlayerMetadataRefresh } from "../player-metadata-refresh";
import { AppShell } from "./app-shell";

/**
 * 已登录布局。以会话代次为 key：换用户或重新登录时整个子树重新挂载，丢弃表单、对话框等私有状态；
 * 会话结束的同一次渲染里先卸载子树，子树内的 hook 不会看到未登录状态。收藏与歌单的跨标签页刷新、播放器队列显示元数据的刷新（M5 ⑤）在这里挂一次。
 */
export function AuthenticatedLayout() {
  const session = useSession();
  if (session.status !== "authenticated") return null;
  return (
    <ViewerScopeProvider key={session.generation} viewer={session.viewer} generation={session.generation}>
      <PersonalSync>
        <PlayerProvider userId={session.viewer.id}>
          <PlayerMetadataRefresh>
            <AppShell>
              <Outlet />
            </AppShell>
          </PlayerMetadataRefresh>
        </PlayerProvider>
      </PersonalSync>
    </ViewerScopeProvider>
  );
}
