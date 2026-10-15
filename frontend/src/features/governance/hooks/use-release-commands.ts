import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { failureOf, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import type { CorrectableField, CorrectionResultDTO, GroupChangeResultDTO, GroupDefaultResultDTO, RevertResultDTO } from "../api/decoders";
import { changeReleaseGroup, correctRelease, revertOperation, setGroupDefault, type CorrectionChanges } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/** 命令作用的发行：id 用来发请求，title 只用来写提示文字。 */
export type ReleaseTarget = { id: string; title: string };

/** 被撤销的操作是哪一种：成功提示据此说明恢复了什么。group 是从操作记录里撤销的归组调整（记录不区分归入与移出），default 是默认版本的调整。 */
export type RevertSubject = "correct" | "join" | "detach" | "group" | "default" | "other";

export type ReleaseIntent =
  | { kind: "correct"; release: ReleaseTarget; revision: number; changes: CorrectionChanges; fields: readonly CorrectableField[] }
  | { kind: "join"; release: ReleaseTarget; revision: number; group: { id: string; title: string } }
  | { kind: "detach"; release: ReleaseTarget; revision: number }
  | {
      /**
       * 设定或清除专辑的默认版本。release 是页面上正在看的版本（成功之后页面继续显示它）；group 是专辑与页面读到的专辑版本号
       * （预期版本）；target 是要设为默认版本的发行（设为默认版本时就是 release 自己），null 是改回自动选择。
       */
      kind: "default";
      release: ReleaseTarget;
      group: { id: string; revision: number };
      target: string | null;
    }
  | {
      kind: "revert";
      release: ReleaseTarget;
      operationId: string;
      subject: RevertSubject;
      /** 撤销归组或默认版本调整之后页面要显示的专辑（归组是原来的组，默认版本是这张专辑）；字段修正的撤销没有。操作记录页不跟随（它的 onMoved 什么也不做）。 */
      returnGroupId?: string | undefined;
    };

/**
 * 一次操作意图：幂等键在发起时生成。结果不确定（连接中断、5xx）后的“重试”沿用同一个命令与同一个键，
 * 后端据此重放第一次的结果而不是再执行一遍；换了意图（改了输入、换了目标）就是新命令、新键。
 */
export type ReleaseCommand = ReleaseIntent & { key: string };

export type ReleaseCommandResult =
  | { kind: "correct"; result: CorrectionResultDTO }
  | { kind: "join"; result: GroupChangeResultDTO; groupTitle: string }
  | { kind: "detach"; result: GroupChangeResultDTO }
  | { kind: "default"; result: GroupDefaultResultDTO }
  | { kind: "revert"; result: RevertResultDTO };

export type ReleaseCommandState =
  | { status: "idle" }
  | { status: "pending"; command: ReleaseCommand }
  | { status: "succeeded"; command: ReleaseCommand; result: ReleaseCommandResult }
  | { status: "failed"; command: ReleaseCommand; error: unknown; failure: ApiFailure | null; uncertain: boolean };

export type ReleaseCommandCallbacks = {
  onSucceeded?: (result: ReleaseCommandResult) => void;
  /** uncertain 为 true 时操作可能已经执行：先刷新核对，可以用同一个键重试；否则是明确的拒绝，需要重新发起。 */
  onFailed?: (failure: ApiFailure | null, uncertain: boolean) => void;
};

type ReleaseCommandsOptions = {
  /** 修改成功或失败之后刷新别处缓存的专辑数据（目录、最近播放……）；由 app 组合，governance 不知道那些查询键。 */
  refresh: () => Promise<unknown>;
  /**
   * 页面应该显示的专辑组与版本：版本换了专辑组（归入、移出，或撤销它们）之后，以及设定、清除默认版本之后（专辑不变，
   * 代表版本可能变了，页面要继续显示刚才那个版本）；在刷新之前同步调用。先导航再刷新：
   * 否则刷新读到“原来的专辑已经没有这个版本（甚至已经空了）”，页面会闪一下“专辑不存在”，还会与详情页自己的地址规范化争抢导航。
   * 由 app 实现导航，governance 不依赖路由。
   */
  onMoved: (groupId: string, releaseId: string) => void;
};

/**
 * 管理员对发行与专辑的写操作：修正字段、归入其他专辑、移出为独立专辑、设定或清除默认版本与撤销。悲观更新：只在后端确认并刷新相关查询之后才算完成，
 * 不做乐观修改；无论成败（包括 409 冲突与结果不确定）都刷新，让界面显示的是服务器当前的状态。
 * 同一时刻只有一个命令；写操作从不自动重试，重试由用户显式触发。
 */
export function useReleaseCommands({ refresh, onMoved }: ReleaseCommandsOptions) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation<ReleaseCommandResult, unknown, ReleaseCommand>({
    mutationFn: async (command) => {
      switch (command.kind) {
        case "correct": {
          const { data } = await correctRelease(command.release.id, command.revision, command.changes, command.key);
          return { kind: "correct", result: data };
        }
        case "join": {
          const { data } = await changeReleaseGroup(command.release.id, command.revision, { action: "join", group_id: command.group.id }, command.key);
          return { kind: "join", result: data, groupTitle: command.group.title };
        }
        case "detach": {
          const { data } = await changeReleaseGroup(command.release.id, command.revision, { action: "detach" }, command.key);
          return { kind: "detach", result: data };
        }
        case "default": {
          const { data } = await setGroupDefault(command.group.id, command.group.revision, command.target, command.key);
          return { kind: "default", result: data };
        }
        case "revert": {
          const { data } = await revertOperation(command.operationId, command.key);
          return { kind: "revert", result: data };
        }
      }
    },
    // 成功之后、刷新之前：版本换了专辑组就先把页面带到新的位置（旧身份的迟到结果不导航）。
    onSuccess: (result, command) => {
      if (!scope.isCurrent()) return;
      if (result.kind === "join" || result.kind === "detach") onMoved(result.result.group_id, command.release.id);
      else if (result.kind === "default") onMoved(result.result.group_id, command.release.id);
      else if (command.kind === "revert" && command.returnGroupId !== undefined) onMoved(command.returnGroupId, command.release.id);
    },
    // 返回 Promise：相关查询刷新完成后命令才算结束，成功提示出现时页面已经是新的。
    onSettled: () => Promise.all([queryClient.invalidateQueries({ queryKey: governanceKeys.all(generation) }), refresh()]),
  });

  const { status, variables, data, error, mutate, reset } = mutation;
  const state = useMemo((): ReleaseCommandState => {
    switch (status) {
      case "idle":
        return { status: "idle" };
      case "pending":
        return { status: "pending", command: variables };
      case "success":
        return { status: "succeeded", command: variables, result: data };
      case "error": {
        const failure = failureOf(error);
        return { status: "failed", command: variables, error, failure, uncertain: isOutcomeUncertain(failure) };
      }
    }
  }, [status, variables, data, error]);

  // 回调只在发起命令的会话仍是当前会话时才触发：会话已结束或被替换后，迟到的结果不能再更新界面或导航。
  const mutateOptions = useCallback(
    (callbacks: ReleaseCommandCallbacks | undefined): Parameters<typeof mutate>[1] => ({
      onSuccess: (result) => {
        if (scope.isCurrent()) callbacks?.onSucceeded?.(result);
      },
      onError: (failure) => {
        if (!scope.isCurrent()) return;
        const classified = failureOf(failure);
        callbacks?.onFailed?.(classified, isOutcomeUncertain(classified));
      },
    }),
    [scope],
  );
  const submit = useCallback(
    (intent: ReleaseIntent, callbacks?: ReleaseCommandCallbacks) => mutate({ ...intent, key: newIdempotencyKey() }, mutateOptions(callbacks)),
    [mutate, mutateOptions],
  );
  const retry = useCallback(
    (callbacks?: ReleaseCommandCallbacks) => {
      if (status === "error") mutate(variables, mutateOptions(callbacks));
    },
    [status, variables, mutate, mutateOptions],
  );

  // 返回的对象只在状态变化时换新：页面可以把它放进 useCallback / useMemo 的依赖。
  return useMemo(
    () => ({
      state,
      /** 发起新的操作意图，生成新的幂等键。 */
      submit,
      /** 沿用上一个命令（同一个键）再发一次；只在上一次失败时有意义。 */
      retry,
      /** 清除上一次的结果，回到没有命令的状态。 */
      dismiss: reset,
    }),
    [state, submit, retry, reset],
  );
}

export type ReleaseCommands = ReturnType<typeof useReleaseCommands>;
