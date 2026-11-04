import { useMutation } from "@tanstack/react-query";
import { useRef } from "react";
import { failureOf, isOutcomeUncertain } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";

export type CommandState =
  | { status: "idle" }
  | { status: "pending" }
  /** uncertain 为 true 时服务端可能已经执行：只能用同一个键、同一个请求重试（retry），不能当成失败换个键再发。 */
  | { status: "failed"; error: unknown; uncertain: boolean };

export type CommandCallbacks<R> = {
  onSucceeded?: (result: R) => void;
  onFailed?: (error: unknown) => void;
};

type Attempt<V> = { input: V; key: string };

/**
 * 带幂等键的歌单写命令：一次用户意图生成一个键；“重试”沿用上一次的键与请求，服务端据此只执行一次，重放返回首次的结果。
 * 回调只在发起时的会话仍是当前会话时触发。成功与失败之后要失效什么由调用方在 onSettled 里决定。
 */
export function useIdempotentCommand<V, R>(send: (input: V, key: string) => Promise<R>, options: { onSucceeded?: (result: R, input: V) => void; onSettled?: () => void } = {}) {
  const scope = useViewerScope();
  const last = useRef<Attempt<V> | null>(null);
  const mutation = useMutation({
    mutationFn: (attempt: Attempt<V>) => send(attempt.input, attempt.key),
    onSuccess: (result, attempt) => options.onSucceeded?.(result, attempt.input),
    onSettled: () => options.onSettled?.(),
  });
  const state: CommandState =
    mutation.status === "pending"
      ? { status: "pending" }
      : mutation.status === "error"
        ? { status: "failed", error: mutation.error, uncertain: isOutcomeUncertain(failureOf(mutation.error)) }
        : { status: "idle" };
  function submit(attempt: Attempt<V>, callbacks?: CommandCallbacks<R>) {
    last.current = attempt;
    mutation.mutate(attempt, {
      onSuccess: (result) => {
        if (scope.isCurrent()) callbacks?.onSucceeded?.(result);
      },
      onError: (error) => {
        if (scope.isCurrent()) callbacks?.onFailed?.(error);
      },
    });
  }
  return {
    state,
    /** 发起一个新的意图（新的幂等键）。 */
    run: (input: V, callbacks?: CommandCallbacks<R>) => submit({ input, key: newIdempotencyKey() }, callbacks),
    /** 用上一次的键与请求重试（结果不确定之后）。 */
    retry: (callbacks?: CommandCallbacks<R>) => {
      if (last.current !== null) submit(last.current, callbacks);
    },
    /** 回到没有命令的状态。 */
    dismiss: () => {
      last.current = null;
      mutation.reset();
    },
  };
}
