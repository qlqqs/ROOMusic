import { Outlet } from "@tanstack/react-router";
import { useSession, ViewerScopeProvider } from "../../features/auth";
import { AppShell } from "./app-shell";

/**
 * 已登录布局。以会话代次为 key：换用户或重新登录时整个子树重新挂载，丢弃表单、对话框等私有状态；
 * 会话结束的同一次渲染里先卸载子树，子树内的 hook 不会看到未登录状态。
 */
export function AuthenticatedLayout() {
  const session = useSession();
  if (session.status !== "authenticated") return null;
  return (
    <ViewerScopeProvider key={session.generation} viewer={session.viewer} generation={session.generation}>
      <AppShell>
        <Outlet />
      </AppShell>
    </ViewerScopeProvider>
  );
}
