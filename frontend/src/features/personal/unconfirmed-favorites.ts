import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";

// 结果不确定的心形切换（网络断了、无效响应、5xx：写请求可能已经执行）：在重新读到确定的收藏状态之前，
// 所有位置的同一目标都不能再切换，否则可能在不知道服务端状态的情况下又发一次写。
// 按 QueryClient 保存（与查询缓存同生命周期），键里带会话代次；不持久化。

type Marks = ReadonlyMap<string, number>;

// 单调递增的序号：标记与每次批量状态读取的开始各取一个。只有在标记之后才开始的读取能确认目标，
// 早于标记开始、晚于标记返回的读取（可能读到写之前的状态）不算。不用时钟，同一毫秒里的先后也分得清。
let clock = 0;

/** 取下一个序号。 */
export function nextFavoriteTick(): number {
  clock += 1;
  return clock;
}

type Store = { marks: Marks; listeners: Set<() => void> };

const stores = new WeakMap<QueryClient, Store>();
const empty: Marks = new Map();

function storeOf(client: QueryClient): Store {
  let store = stores.get(client);
  if (store === undefined) {
    store = { marks: empty, listeners: new Set() };
    stores.set(client, store);
  }
  return store;
}

function update(client: QueryClient, change: (marks: Map<string, number>) => boolean) {
  const store = storeOf(client);
  const next = new Map(store.marks);
  if (!change(next)) return;
  store.marks = next;
  for (const listener of store.listeners) listener();
}

/** 目标的键：会话代次加目标，ID 小写。 */
export function unconfirmedKey(generation: number, kind: string, id: string): string {
  return `${generation}:${kind}:${id.toLowerCase()}`;
}

/** 记下一个结果不确定的目标：只有在这之后开始、并且成功的读取才算确认。 */
export function markUnconfirmed(client: QueryClient, key: string) {
  const at = nextFavoriteTick();
  update(client, (marks) => {
    marks.set(key, at);
    return true;
  });
}

/** 重新读到了确定的状态：解除这些目标。 */
export function confirmFavorites(client: QueryClient, keys: readonly string[]) {
  update(client, (marks) => keys.reduce((changed, key) => marks.delete(key) || changed, false));
}

/** 当前结果不确定的目标（键 → 记下时的序号）。 */
export function useUnconfirmedFavorites(): Marks {
  const client = useQueryClient();
  const store = storeOf(client);
  return useSyncExternalStore(
    (listener) => {
      store.listeners.add(listener);
      return () => store.listeners.delete(listener);
    },
    () => store.marks,
  );
}
