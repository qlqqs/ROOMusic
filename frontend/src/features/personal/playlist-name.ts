import { MAX_PLAYLIST_NAME } from "./api/decoders";

/** 控制字符（C0、DEL 与 C1）：名字里不能有，后端同样拒绝。 */
function isControl(character: string): boolean {
  const code = character.codePointAt(0) ?? 0;
  return code <= 0x1f || (code >= 0x7f && code <= 0x9f);
}

/** 名字规范化：去掉首尾空白，与后端相同。 */
export function normalizePlaylistName(value: string): string {
  return value.trim();
}

/** 名字的问题；没有问题时为 null。长度按 Unicode 码点计算（与后端一致），不是 UTF-16 长度。 */
export function playlistNameProblem(value: string): string | null {
  const name = normalizePlaylistName(value);
  if (name === "") return "请输入名字。";
  if ([...name].length > MAX_PLAYLIST_NAME) return `名字最多 ${MAX_PLAYLIST_NAME} 个字。`;
  if ([...name].some(isControl)) return "名字里有不能用的字符。";
  return null;
}

/** “存为歌单”预填的名字：操作者本地的日期，如“队列 2026-11-04”。 */
export function queuePlaylistName(now: Date = new Date()): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `队列 ${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}
