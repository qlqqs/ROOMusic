// 用户正停在哪些会话里（对话区挂载着，面板或整页都算）：运行结束时如果用户还在这个会话里，先标记已读再刷新未读数，
// 入口的提示点不会一闪而过（C10）。只在内存里，随对话区的挂载与卸载登记。

const viewing = new Map<string, number>();

/** 对话区挂载时登记，返回的函数在卸载时调用。同一会话可以同时有两处（面板与整页切换的瞬间）。 */
export function markViewing(conversationId: string): () => void {
  viewing.set(conversationId, (viewing.get(conversationId) ?? 0) + 1);
  return () => {
    const count = (viewing.get(conversationId) ?? 0) - 1;
    if (count <= 0) viewing.delete(conversationId);
    else viewing.set(conversationId, count);
  };
}

export function isViewing(conversationId: string): boolean {
  return viewing.has(conversationId);
}
