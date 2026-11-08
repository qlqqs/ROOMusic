import { useMutation, useQueryClient } from "@tanstack/react-query";
import { failureOf, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import type { RootChangedDTO, RootCreatedDTO, RootDTO, RootExclusionsDTO } from "../api/decoders";
import { addRootExclusion, createRoot, disableRoot, removeRootExclusion, restoreRoot } from "../api/endpoints";
import { libraryKeys } from "../query-keys";
import { rootsQueryOptions } from "./use-roots";

/** 界面上看到的目录：停用与恢复必须带这里的 revision，目录在此期间变化时后端返回 409。 */
export type RootTarget = Pick<RootDTO, "id" | "name" | "revision">;

/**
 * exclude 的 path 是目录浏览器给出的绝对路径，unexclude 的 relativePath 取自目录的 excluded_paths。
 * 两者不看 revision：排除项是集合，重复添加或移除都返回当前状态。
 */
export type RootIntent =
  | { kind: "create"; path: string }
  | { kind: "disable"; root: RootTarget }
  | { kind: "restore"; root: RootTarget }
  | { kind: "exclude"; root: RootTarget; path: string }
  | { kind: "unexclude"; root: RootTarget; relativePath: string };

/**
 * 一次操作意图：幂等键在发起时生成。结果不确定（连接中断等）后的“重试”沿用同一个命令与同一个键，
 * 后端据此重放第一次的结果而不是再执行一遍；换了意图（路径或目标变了）就是新命令、新键。
 */
export type RootCommand = RootIntent & { key: string };

export type RootCommandResult =
  | { kind: "create"; root: RootCreatedDTO; alreadyListed: boolean }
  | { kind: "disable" | "restore"; name: string; root: RootChangedDTO }
  | { kind: "exclude" | "unexclude"; root: RootExclusionsDTO };

export type RootCommandState =
  | { status: "idle" }
  | { status: "pending"; command: RootCommand }
  | { status: "succeeded"; command: RootCommand; result: RootCommandResult }
  | { status: "failed"; command: RootCommand; error: unknown; failure: ApiFailure | null; uncertain: boolean };

export type RootCommandCallbacks = {
  onSucceeded?: () => void;
  /** uncertain 为 true 时操作可能已经执行：先刷新核对，可以用同一个键重试；否则是明确的拒绝，需要重新发起。 */
  onFailed?: (failure: ApiFailure | null, uncertain: boolean) => void;
};

/**
 * 目录的新增、停用、恢复与不扫描的子文件夹的增删。悲观更新：只在后端确认并刷新列表之后才算完成，不做乐观修改；
 * 无论成败（包括 409 冲突与结果不确定）都刷新目录列表与操作记录，让界面显示的是服务器当前的状态。
 * 同一时刻只有一个命令；写操作从不自动重试，重试由用户显式触发。
 */
export function useRootCommands() {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation<RootCommandResult, unknown, RootCommand>({
    mutationFn: async (command) => {
      switch (command.kind) {
        case "create": {
          const { data } = await createRoot(command.path, command.key);
          // 响应到达时列表缓存还是操作前的：用它区分“新登记”与“本来就在列表里”。
          const listed = queryClient.getQueryData(rootsQueryOptions(generation).queryKey)?.data.items ?? [];
          return { kind: "create", root: data, alreadyListed: listed.some((root) => root.id === data.id) };
        }
        case "disable": {
          const { data } = await disableRoot(command.root.id, command.root.revision, command.key);
          return { kind: "disable", name: command.root.name, root: data };
        }
        case "restore": {
          const { data } = await restoreRoot(command.root.id, command.root.revision, command.key);
          return { kind: "restore", name: command.root.name, root: data };
        }
        case "exclude": {
          const { data } = await addRootExclusion(command.root.id, command.path, command.key);
          return { kind: "exclude", root: data };
        }
        case "unexclude": {
          const { data } = await removeRootExclusion(command.root.id, command.relativePath, command.key);
          return { kind: "unexclude", root: data };
        }
      }
    },
    // 返回 Promise：列表刷新完成后命令才算结束，成功提示出现时列表已经是新的。
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: libraryKeys.roots(generation) }),
        queryClient.invalidateQueries({ queryKey: libraryKeys.rootOperations(generation) }),
        queryClient.invalidateQueries({ queryKey: libraryKeys.folderListings(generation) }),
      ]),
  });

  const state = ((): RootCommandState => {
    switch (mutation.status) {
      case "idle":
        return { status: "idle" };
      case "pending":
        return { status: "pending", command: mutation.variables };
      case "success":
        return { status: "succeeded", command: mutation.variables, result: mutation.data };
      case "error": {
        const failure = failureOf(mutation.error);
        return { status: "failed", command: mutation.variables, error: mutation.error, failure, uncertain: isOutcomeUncertain(failure) };
      }
    }
  })();

  // 回调只在发起命令的会话仍是当前会话时才触发：会话已结束或被替换后，迟到的结果不能再更新界面。
  const mutateOptions = (callbacks: RootCommandCallbacks | undefined): Parameters<typeof mutation.mutate>[1] => ({
    onSuccess: () => {
      if (scope.isCurrent()) callbacks?.onSucceeded?.();
    },
    onError: (error) => {
      if (!scope.isCurrent()) return;
      const failure = failureOf(error);
      callbacks?.onFailed?.(failure, isOutcomeUncertain(failure));
    },
  });

  return {
    state,
    /** 发起新的操作意图，生成新的幂等键。 */
    submit: (intent: RootIntent, callbacks?: RootCommandCallbacks) =>
      mutation.mutate({ ...intent, key: newIdempotencyKey() }, mutateOptions(callbacks)),
    /** 沿用上一个命令（同一个键）再发一次；只在上一次失败时有意义。 */
    retry: (callbacks?: RootCommandCallbacks) => {
      if (mutation.status === "error") mutation.mutate(mutation.variables, mutateOptions(callbacks));
    },
    /** 清除上一次的结果，回到没有命令的状态。 */
    dismiss: () => mutation.reset(),
  };
}

export type RootCommands = ReturnType<typeof useRootCommands>;
