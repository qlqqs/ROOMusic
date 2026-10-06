import type { StorageLike } from "../features/player/engine/persistence";

/** 内存里的 Storage 替身；data 让测试直接查看写进去的内容。 */
export function memoryStorage(initial: Record<string, string> = {}): StorageLike & { data: Map<string, string> } {
  const data = new Map(Object.entries(initial));
  return {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => void data.set(key, value),
    removeItem: (key) => void data.delete(key),
  };
}
