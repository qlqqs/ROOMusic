import type { MatchField, MatchRole, SearchMatchDTO } from "./api/decoders";

// “匹配到了什么”的小字：命中的不是这一行已经显示的文字时（例如作曲者、厂牌、编号），用一小段“字段：原文”说明，
// 不加别的解释，不做高亮（规范化会改变字数，按偏移高亮会错位）。

// 发行艺人与专辑艺人都叫“专辑艺人”（I6）。
const roleNames: Readonly<Record<MatchRole, string>> = {
  release_artist: "专辑艺人",
  album_artist: "专辑艺人",
  track_artist: "曲目艺人",
  composer: "作曲",
  conductor: "指挥",
  performer: "演奏",
  producer: "制作人",
};

const fieldNames: Readonly<Record<MatchField, string>> = {
  release_title: "专辑",
  release_artist: "专辑艺人",
  album_artist: "专辑艺人",
  track_title: "曲目",
  track_artist: "曲目艺人",
  credit: "署名",
  label: "厂牌",
  catalog_number: "编号",
  artist_name: "艺人",
  artist_alias: "别名",
};

function labelOf(match: SearchMatchDTO): string {
  if (match.field === "artist_alias") return fieldNames.artist_alias;
  if (match.field === "credit" && match.role !== null) return roleNames[match.role];
  return fieldNames[match.field];
}

/**
 * 一条结果的命中说明；不需要说明时是 null。shown 是这一行已经显示的文字（标题、艺人、专辑名）：
 * 命中的原文就是其中之一时不再重复。别名命中写匹配到的别名（“别名：Teresa Teng”）。
 */
export function matchNote(match: SearchMatchDTO, shown: readonly string[]): string | null {
  const text = match.field === "artist_alias" ? (match.alias ?? match.original) : match.original;
  if (text.trim() === "") return null;
  if (match.field !== "artist_alias" && shown.includes(text)) return null;
  return `${labelOf(match)}：${text}`;
}
