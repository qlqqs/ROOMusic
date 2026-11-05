import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { failureOf, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import type { ArtistIgnoreResultDTO, ArtistMergeResultDTO, ArtistRenameResultDTO, ArtistSplitResultDTO } from "../api/decoders";
import { ignoreArtistCandidate, mergeArtist, renameArtist, splitArtist, type ArtistSplitScope, type ArtistSplitTarget } from "../api/endpoints";
import { governanceKeys } from "../query-keys";

/** 命令作用的艺人：id 用来发请求，name 只用来写提示文字。 */
export type ArtistTarget = { id: string; name: string };

export type ArtistIntent =
  | {
      /** 把 source 并入 artist：两边都带页面读到的人工修改版本号。 */
      kind: "merge";
      artist: ArtistTarget;
      revision: number;
      source: ArtistTarget;
      sourceRevision: number;
    }
  | {
      /** 拆开：范围与预览给的依据；targetName 是新艺人（或恢复的那位）的名字，只用来写提示。 */
      kind: "split";
      artist: ArtistTarget;
      revision: number;
      basis: string;
      scope: ArtistSplitScope;
      target: ArtistSplitTarget;
      targetName: string;
    }
  | { kind: "rename"; artist: ArtistTarget; revision: number; name: { mode: "set"; name: string } | { mode: "auto" } }
  | {
      /** 忽略（或恢复）一条“可能是同一位”的提示。 */
      kind: "ignore";
      artist: ArtistTarget;
      revision: number;
      other: ArtistTarget;
      otherRevision: number;
      keyPairs: readonly { left_key_id: string; right_key_id: string }[];
      ignored: boolean;
    };

/** 一次操作意图：幂等键在发起时生成，结果不确定后的“重试”沿用同一个键；换了意图就是新键。 */
export type ArtistCommand = ArtistIntent & { key: string };

export type ArtistCommandResult =
  | { kind: "merge"; result: ArtistMergeResultDTO }
  | { kind: "split"; result: ArtistSplitResultDTO }
  | { kind: "rename"; result: ArtistRenameResultDTO }
  | { kind: "ignore"; result: ArtistIgnoreResultDTO };

export type ArtistCommandState =
  | { status: "idle" }
  | { status: "pending"; command: ArtistCommand }
  | { status: "succeeded"; command: ArtistCommand; result: ArtistCommandResult }
  | { status: "failed"; command: ArtistCommand; error: unknown; failure: ApiFailure | null; uncertain: boolean };

export type ArtistCommandCallbacks = {
  onSucceeded?: (result: ArtistCommandResult) => void;
  onFailed?: (failure: ApiFailure | null, uncertain: boolean) => void;
};

type ArtistCommandsOptions = {
  /** 治理之后刷新别处缓存的数据（艺人、目录里的艺人引用、搜索、收藏的艺人……）；由 app 组合，governance 不知道那些查询键。 */
  refresh: () => Promise<unknown>;
};

/**
 * 管理员对艺人的写操作：合并、拆开、改名与忽略提示（M5 ⑤，都接入受治理操作、不能撤销，反向操作就是恢复的办法）。
 * 与发行的命令相同的模型：悲观更新，后端确认并刷新之后才算完成；无论成败都刷新；同一时刻只有一个命令；不自动重试。
 */
export function useArtistCommands({ refresh }: ArtistCommandsOptions) {
  const queryClient = useQueryClient();
  const scope = useViewerScope();
  const { generation } = scope;

  const mutation = useMutation<ArtistCommandResult, unknown, ArtistCommand>({
    mutationFn: async (command) => {
      switch (command.kind) {
        case "merge": {
          const { data } = await mergeArtist(command.artist.id, { expectedRevision: command.revision, sourceId: command.source.id, sourceExpectedRevision: command.sourceRevision }, command.key);
          return { kind: "merge", result: data };
        }
        case "split": {
          const { data } = await splitArtist(command.artist.id, { expectedRevision: command.revision, basis: command.basis, scope: command.scope, target: command.target }, command.key);
          return { kind: "split", result: data };
        }
        case "rename": {
          const { data } = await renameArtist(command.artist.id, { expectedRevision: command.revision, name: command.name }, command.key);
          return { kind: "rename", result: data };
        }
        case "ignore": {
          const { data } = await ignoreArtistCandidate(
            command.artist.id,
            { expectedRevision: command.revision, otherId: command.other.id, otherExpectedRevision: command.otherRevision, keyPairs: command.keyPairs, ignored: command.ignored },
            command.key,
          );
          return { kind: "ignore", result: data };
        }
      }
    },
    onSettled: () => Promise.all([queryClient.invalidateQueries({ queryKey: governanceKeys.all(generation) }), refresh()]),
  });

  const { status, variables, data, error, mutate, reset } = mutation;
  const state = useMemo((): ArtistCommandState => {
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

  const mutateOptions = useCallback(
    (callbacks: ArtistCommandCallbacks | undefined): Parameters<typeof mutate>[1] => ({
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
    (intent: ArtistIntent, callbacks?: ArtistCommandCallbacks) => mutate({ ...intent, key: newIdempotencyKey() }, mutateOptions(callbacks)),
    [mutate, mutateOptions],
  );
  const retry = useCallback(
    (callbacks?: ArtistCommandCallbacks) => {
      if (status === "error") mutate(variables, mutateOptions(callbacks));
    },
    [status, variables, mutate, mutateOptions],
  );

  return useMemo(() => ({ state, submit, retry, dismiss: reset }), [state, submit, retry, reset]);
}

export type ArtistCommands = ReturnType<typeof useArtistCommands>;
