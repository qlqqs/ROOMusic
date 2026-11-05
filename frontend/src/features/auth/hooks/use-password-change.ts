import { useMutation } from "@tanstack/react-query";
import { changeOwnPassword, type PasswordChange } from "../api/endpoints";
import { useViewerScope } from "../session/session-context";

export type PasswordChangeCallbacks = {
  onSucceeded?: () => void;
  onFailed?: (error: unknown) => void;
};

/**
 * 修改自己的密码。成功后什么都不清：查看者没变（id 与 role 相同），不增代次、不清查询缓存、不广播；
 * 同一浏览器的其他标签页共用这个 Cookie，会话仍然有效，其他浏览器的会话已被后端撤销，它们下一次请求得到 401，
 * 由各自的会话边界回到登录页。写请求不自动重试；回调只在发起时的会话仍是当前会话时才触发。
 */
export function usePasswordChange() {
  const scope = useViewerScope();
  const mutation = useMutation({ mutationFn: (input: PasswordChange) => changeOwnPassword(input) });
  return {
    pending: mutation.isPending,
    submit: (input: PasswordChange, callbacks: PasswordChangeCallbacks = {}) =>
      mutation.mutate(input, {
        onSuccess: () => {
          if (scope.isCurrent()) callbacks.onSucceeded?.();
        },
        onError: (error) => {
          if (scope.isCurrent()) callbacks.onFailed?.(error);
        },
      }),
    reset: () => mutation.reset(),
  };
}

export type PasswordChangeMutation = ReturnType<typeof usePasswordChange>;
