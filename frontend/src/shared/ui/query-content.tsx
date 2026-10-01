import type { ReactNode } from "react";
import { Button } from "./button";
import { FailureNotice } from "./failure-notice";

/** 查询结果的最小结构形状：shared/ui 不依赖具体的查询库。 */
export type QuerySource<T> = {
  status: "pending" | "error" | "success";
  data: T | undefined;
  error: unknown;
  refetch: () => unknown;
};

type QueryContentProps<T> = {
  query: QuerySource<T>;
  /** 首次加载的占位（保留区块结构，不画成空库）。 */
  loading: ReactNode;
  children: (data: T) => ReactNode;
};

/**
 * 数据区块的四种状态：首次加载、失败、成功、以及“刷新失败但还有旧数据”。
 * 失败时给出带请求 ID 的错误和重试；有旧数据时继续显示旧数据，错误在其上方。
 */
export function QueryContent<T>({ query, loading, children }: QueryContentProps<T>) {
  const retry = (
    <Button onClick={() => void query.refetch()}>重试</Button>
  );
  if (query.status === "pending") return <>{loading}</>;
  if (query.status === "error") {
    return (
      <>
        <FailureNotice error={query.error} action={retry} />
        {query.data === undefined ? null : children(query.data)}
      </>
    );
  }
  return query.data === undefined ? null : <>{children(query.data)}</>;
}
