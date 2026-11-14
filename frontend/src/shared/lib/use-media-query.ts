import { useCallback, useSyncExternalStore } from "react";

/**
 * 同一个媒体查询在页面里共用一个 MediaQueryList 与一个 change 监听：曲目表可能有上万行，每行都订阅时不各建一个。
 * 最后一个订阅者离开时删掉，之后再订阅会重新创建（测试可以在两次渲染之间替换 window.matchMedia）。
 */
type Entry = { list: MediaQueryList; listeners: Set<() => void>; onChange: () => void };

const entries = new Map<string, Entry>();

function subscribeTo(query: string, listener: () => void): () => void {
  let entry = entries.get(query);
  if (entry === undefined) {
    const listeners = new Set<() => void>();
    const created: Entry = { list: window.matchMedia(query), listeners, onChange: () => listeners.forEach((notify) => notify()) };
    created.list.addEventListener("change", created.onChange);
    entries.set(query, created);
    entry = created;
  }
  entry.listeners.add(listener);
  const current = entry;
  return () => {
    current.listeners.delete(listener);
    if (current.listeners.size > 0) return;
    current.list.removeEventListener("change", current.onChange);
    if (entries.get(query) === current) entries.delete(query);
  };
}

/** 媒体查询当前是否成立，变化时重新渲染。 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback((onChange: () => void) => subscribeTo(query, onChange), [query]);
  return useSyncExternalStore(subscribe, () => (entries.get(query)?.list ?? window.matchMedia(query)).matches);
}
