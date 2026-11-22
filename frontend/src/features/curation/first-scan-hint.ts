import type { CurationProgressDTO, CurationSettingsDTO } from "./api/decoders";

/** 一次性提示的本地键：按用户区分；只是这个浏览器上这位查看者的便利，不是要可靠保存的状态。 */
function storageKey(userId: string): string {
  return `roomusic.curation.first-scan-hint.${userId}`;
}

/** 这位用户是否在这个浏览器上关掉过提示。存储不可用（隐私模式、被禁用）时当作没有关过。 */
export function hintDismissed(userId: string): boolean {
  try {
    return window.localStorage.getItem(storageKey(userId)) === "dismissed";
  } catch {
    return false;
  }
}

/** 记下“不再提示”；存储不可用时什么都不做（这次会话里由调用方的状态隐藏）。 */
export function dismissHint(userId: string): void {
  try {
    window.localStorage.setItem(storageKey(userId), "dismissed");
  } catch {
    // 存储不可用：只在这次会话里隐藏。
  }
}

/**
 * 概览上“打开自动整理”的提示：管理员、曲库里已经有发行、自动整理从未打开过（enabled_at 为 null；关掉之后保留打开时的值，所以打开过就不再提示），
 * 并且没有在这个浏览器上关掉过。
 */
export function shouldShowFirstScanHint(input: {
  isAdmin: boolean;
  settings: CurationSettingsDTO | undefined;
  progress: CurationProgressDTO | undefined;
  dismissed: boolean;
}): boolean {
  if (!input.isAdmin || input.dismissed || input.settings === undefined || input.progress === undefined) return false;
  return input.settings.enabled_at === null && !input.settings.enabled && input.progress.releases.total > 0;
}
