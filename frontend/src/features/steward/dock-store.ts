import { useSyncExternalStore } from "react";

// 面板的状态（应用级）：开没开、显示哪个会话、空白新对话预先选的模型。开关记在这个浏览器里；
// 会话 ID、草稿与对话区的提示只在内存里，登出或换用户时由应用调用 resetDock 清掉。

export type DockState = {
  open: boolean;
  /** 面板与整页当前的会话；null 是空白的新对话（发第一条消息时才创建）。只记读取成功过的会话。 */
  conversationId: string | null;
  /** 空白新对话在模型菜单里选的模型；创建会话后写进会话。null 表示用设置里的模型。 */
  newModel: string | null;
  /**
   * 用户在应用里要的是空白新对话（“新对话”、标签栏、展开空白面板、记着的会话已不存在）：/steward 不再转到列表里最近的会话。
   * 打开某个会话后清掉；只在内存里，刷新页面后进 /steward 会转到最近的会话。
   */
  blankIntent: boolean;
  /** 由用户打开面板：面板内容就绪后把焦点移进去（页面载入时按记住的开关打开的不移）。 */
  focusPending: boolean;
};

const OPEN_KEY = "roomusic.steward.dock-open";

let state: DockState = { open: window.localStorage.getItem(OPEN_KEY) === "1", conversationId: null, newModel: null, blankIntent: false, focusPending: false };
const listeners = new Set<() => void>();

function update(next: Partial<DockState>) {
  state = { ...state, ...next };
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getDockState(): DockState {
  return state;
}

export function useDockState(): DockState {
  return useSyncExternalStore(subscribe, () => state);
}

/** focus：由用户的操作打开，面板就绪后把焦点移进去。 */
export function setDockOpen(open: boolean, { focus = false }: { focus?: boolean } = {}): void {
  window.localStorage.setItem(OPEN_KEY, open ? "1" : "0");
  update({ open, focusPending: open && focus });
}

/** 侧栏入口：用户点击开合。 */
export function toggleDock(): void {
  setDockOpen(!state.open, { focus: true });
}

/** 面板已经把焦点移进去了。 */
export function clearDockFocus(): void {
  if (state.focusPending) update({ focusPending: false });
}

export function setDockConversation(conversationId: string | null): void {
  if (conversationId === state.conversationId && (conversationId === null || !state.blankIntent)) return;
  update({ conversationId, newModel: conversationId === null ? state.newModel : null, blankIntent: conversationId === null ? state.blankIntent : false });
}

/** 要空白新对话：清掉当前会话，/steward 不转到最近的会话。 */
export function startBlankConversation(): void {
  update({ conversationId: null, blankIntent: true });
}

/** 标签栏进入：面板没有记着会话时给空白新对话，不转到列表里最近的会话。 */
export function preferBlankConversation(): void {
  if (!state.blankIntent) update({ blankIntent: true });
}

export function setNewConversationModel(model: string | null): void {
  update({ newModel: model });
}

// ---- 草稿与对话区的提示：按会话 ID 存（空白新对话用 "new"），面板与整页共用；可订阅，
// 新对话创建后对话区按新会话重新挂载，之前的请求失败时写回的文字与提示也能显示出来。----

/** 对话区里就近显示的失败：新建会话失败；切换模型失败；新对话建好了但预选的模型没有写进去（消息还没发）。 */
export type ViewNotice = { action: "create" | "model" | "new_model"; error: unknown };

const drafts = new Map<string, string>();
const notices = new Map<string, ViewNotice>();
const entryListeners = new Set<() => void>();

function notifyEntries() {
  for (const listener of entryListeners) listener();
}

function subscribeEntries(listener: () => void): () => void {
  entryListeners.add(listener);
  return () => entryListeners.delete(listener);
}

export function draftOf(key: string): string {
  return drafts.get(key) ?? "";
}

export function saveDraft(key: string, text: string): void {
  if (text === draftOf(key)) return;
  if (text === "") drafts.delete(key);
  else drafts.set(key, text);
  notifyEntries();
}

export function useDraft(key: string): string {
  return useSyncExternalStore(subscribeEntries, () => draftOf(key));
}

export function setViewNotice(key: string, notice: ViewNotice | null): void {
  if (notice === null && !notices.has(key)) return;
  if (notice === null) notices.delete(key);
  else notices.set(key, notice);
  notifyEntries();
}

export function useViewNotice(key: string): ViewNotice | null {
  return useSyncExternalStore(subscribeEntries, () => notices.get(key) ?? null);
}

/**
 * resetDock 之后递增。异步操作（发送、建会话）开始时记下，返回后不一致就不再写草稿与提示：
 * 请求得到 401 时会话边界先清空这里，之后才轮到请求的失败处理，旧用户的文字不能留给下一个登录的人。
 */
let epoch = 0;

export function dockEpoch(): number {
  return epoch;
}

/** 登出或换用户：会话、草稿、提示与预选的模型属于上一个人。面板开关是这个浏览器的偏好，保留。 */
export function resetDock(): void {
  epoch += 1;
  drafts.clear();
  notices.clear();
  notifyEntries();
  update({ conversationId: null, newModel: null, blankIntent: false, focusPending: false });
}
