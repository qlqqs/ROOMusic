import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { useViewerScope } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import { createMember, resetMemberPassword, revokeMemberSessions, setMemberDisabled } from "../api/endpoints";
import { memberKeys } from "../query-keys";

export type MemberTarget = Pick<MemberDTO, "id" | "username">;

export type MemberCommand =
  | { kind: "create"; username: string; password: string }
  | { kind: "disable"; member: MemberTarget }
  | { kind: "enable"; member: MemberTarget }
  | { kind: "revoke"; member: MemberTarget }
  /** disabled 记下发起时成员是否停用（沿用合同里的形状；结果文案现在不再区分）。 */
  | { kind: "reset-password"; member: MemberTarget & { disabled: boolean }; password: string };

export type MemberCommandState =
  | { status: "idle" }
  | { status: "pending"; command: MemberCommand }
  | { status: "succeeded"; command: MemberCommand }
  | { status: "failed"; command: MemberCommand; error: unknown; failure: ApiFailure | null; uncertain: boolean };

export type MemberCommandCallbacks = {
  onSucceeded?: () => void;
  /** uncertain 为 true 时操作可能已经执行：列表已刷新，用户核对后再决定是否重新操作。 */
  onFailed?: (failure: ApiFailure | null, uncertain: boolean) => void;
};

async function execute(command: MemberCommand): Promise<void> {
  switch (command.kind) {
    case "create":
      await createMember({ username: command.username, password: command.password });
      return;
    case "disable":
      await setMemberDisabled(command.member.id, true);
      return;
    case "enable":
      await setMemberDisabled(command.member.id, false);
      return;
    case "revoke":
      await revokeMemberSessions(command.member.id);
      return;
    case "reset-password":
      await resetMemberPassword(command.member.id, command.password);
      return;
  }
}

/**
 * 成员的新建、停用、重新启用、撤销会话与重置密码。后端没有幂等键，同一个请求发两次就执行两次，所以这里没有“重试”：
 * 无论成败（包括结果不确定）都刷新成员列表，让用户核对服务器上的当前状态，再决定是否重新操作。
 * 对自己的操作成功后，后端已经撤销了当前会话，随后的列表刷新得到 401，由会话边界回到登录页。
 */
export function useMemberCommands() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation<void, unknown, MemberCommand>({
    mutationFn: execute,
    onSettled: () => queryClient.invalidateQueries({ queryKey: memberKeys.list(generation) }),
  });

  const state = ((): MemberCommandState => {
    switch (mutation.status) {
      case "idle":
        return { status: "idle" };
      case "pending":
        return { status: "pending", command: mutation.variables };
      case "success":
        return { status: "succeeded", command: mutation.variables };
      case "error": {
        const failure = failureOf(mutation.error);
        return { status: "failed", command: mutation.variables, error: mutation.error, failure, uncertain: isOutcomeUncertain(failure) };
      }
    }
  })();

  return {
    state,
    /** 发起命令；回调只在发起命令的会话仍是当前会话时才触发。 */
    submit: (command: MemberCommand, callbacks?: MemberCommandCallbacks) =>
      mutation.mutate(command, {
        onSuccess: () => {
          if (scope.isCurrent()) callbacks?.onSucceeded?.();
        },
        onError: (error) => {
          if (!scope.isCurrent()) return;
          const failure = failureOf(error);
          callbacks?.onFailed?.(failure, isOutcomeUncertain(failure));
        },
      }),
    /** 清除上一次的结果，回到没有命令的状态。 */
    dismiss: () => mutation.reset(),
  };
}

export type MemberCommands = ReturnType<typeof useMemberCommands>;
