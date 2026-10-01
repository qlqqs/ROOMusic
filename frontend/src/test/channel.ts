import type { SessionChannel } from "../features/auth";

/** 在内存里连通两个“标签页”的会话通道：一方通知，另一方收到（自己收不到自己的）。 */
export function channelPair(): [SessionChannel, SessionChannel] {
  const handlers: [Set<() => void>, Set<() => void>] = [new Set(), new Set()];
  const make = (self: 0 | 1): SessionChannel => ({
    notifyChanged: () => handlers[self === 0 ? 1 : 0].forEach((handler) => handler()),
    onChanged: (handler) => {
      handlers[self].add(handler);
      return () => handlers[self].delete(handler);
    },
    close: () => handlers[self].clear(),
  });
  return [make(0), make(1)];
}
