import { useMutation } from "@tanstack/react-query";
import type { Credentials } from "../api/endpoints";
import { useSessionController } from "../session/session-context";

// 登录、初始化与退出都交给会话控制器：它串行执行转换、维护代次，并在会话变化时通知应用清理缓存。
// 这些 mutation 只负责提交中与失败的界面状态；成功后的跳转由路由守卫根据会话状态完成。

export function useLogin() {
  const controller = useSessionController();
  return useMutation({ mutationFn: (credentials: Credentials) => controller.login(credentials) });
}

export function useSetupAdmin() {
  const controller = useSessionController();
  return useMutation({ mutationFn: (credentials: Credentials) => controller.setupAdmin(credentials) });
}

export function useLogout() {
  const controller = useSessionController();
  return useMutation({ mutationFn: () => controller.logout() });
}
