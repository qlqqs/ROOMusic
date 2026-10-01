const CHANNEL_NAME = "roomusic:session";

export type SessionChannel = {
  /** 通知其他标签页“会话已变化”。只发送这一个固定消息，不含凭据与用户数据。 */
  notifyChanged: () => void;
  /** 其他标签页通知会话已变化时调用 handler；返回取消订阅函数。 */
  onChanged: (handler: () => void) => () => void;
  close: () => void;
};

function isChangedMessage(data: unknown): boolean {
  return typeof data === "object" && data !== null && "type" in data && data.type === "changed";
}

/** 浏览器不支持 BroadcastChannel 时返回 null：此时只靠页面重新可见时的核对同步登录状态。 */
export function createBroadcastSessionChannel(name: string = CHANNEL_NAME): SessionChannel | null {
  if (typeof BroadcastChannel === "undefined") return null;
  const channel = new BroadcastChannel(name);
  return {
    notifyChanged: () => channel.postMessage({ type: "changed" }),
    onChanged: (handler) => {
      const listener = (event: MessageEvent<unknown>) => {
        if (isChangedMessage(event.data)) handler();
      };
      channel.addEventListener("message", listener);
      return () => channel.removeEventListener("message", listener);
    },
    close: () => channel.close(),
  };
}
