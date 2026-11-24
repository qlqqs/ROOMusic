import type { ArtistBioDTO, ArtistImageDTO } from "../api/decoders";

export type ArtistMediaSource = ArtistImageDTO["source"] | ArtistBioDTO["source"];

/** 来源的种类：决定印章用哪个图标、什么色调。有许可的外部来源一类，插件一类，网络搜索（模型整理）一类，管理员提供一类。 */
export type ArtistMediaSourceKind = "licensed" | "plugin" | "web" | "manual";

const licensedNames: Record<string, string> = {
  fanarttv: "Fanart.tv",
  theaudiodb: "TheAudioDB",
  commons: "Wikimedia Commons",
  wikipedia: "维基百科",
};

export function sourceKindOf(source: ArtistMediaSource): ArtistMediaSourceKind {
  if (source === "manual") return "manual";
  if (source === "web_search") return "web";
  if (source.startsWith("plugin:")) return "plugin";
  return "licensed";
}

/** 来源的显示名：外部来源用它的名字，插件写“插件 <id>”，管理员提供的写“管理员提供”。 */
export function sourceNameOf(source: ArtistMediaSource): string {
  if (source === "manual") return "管理员提供";
  if (source === "web_search") return "网络搜索";
  if (source.startsWith("plugin:")) return `插件 ${source.slice("plugin:".length)}`;
  return licensedNames[source] ?? source;
}

/** 外部给的地址只在是 http(s) 时做成链接，其余按纯文本显示。 */
export function safeUrl(url: string | null): string | null {
  if (url === null) return null;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : null;
  } catch {
    return null;
  }
}

/** 印章与来源图标的提示：“图片来源：维基百科 · CC BY-SA 4.0”。 */
export function sourceSummaryOf(what: "图片" | "简介", media: { source: ArtistMediaSource; license: string | null }): string {
  const parts = [sourceNameOf(media.source), media.license].filter((part): part is string => part !== null);
  return `${what}来源：${parts.join(" · ")}`;
}

/** 引用网页的简短写法：主机名加路径（去掉协议与 www.）；不是 http(s) 时为 null（不显示）。 */
export function citationLabelOf(url: string): string | null {
  const safe = safeUrl(url);
  if (safe === null) return null;
  const parsed = new URL(safe);
  const path = parsed.pathname === "/" ? "" : decodeURIComponent(parsed.pathname);
  return `${parsed.hostname.replace(/^www\./, "")}${path}`;
}
