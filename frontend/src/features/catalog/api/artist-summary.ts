import { decode as d } from "../../../shared/api";

// 艺人摘要（M5 ⑤）：艺人列表、全局搜索的艺人结果、艺人收藏、“可能是同一位”的候选共用的公开合同。
// 纯类型与解码器，不连带组件与样式：接口样本测试与真实服务用例可以直接导入这个模块。

/** 署名角色（艺人参与的方式），与后端的白名单一致。 */
export const artistRoles = ["release_artist", "album_artist", "track_artist", "composer", "conductor", "performer", "producer"] as const;
export type ArtistRole = (typeof artistRoles)[number];

/** 首字母分组：A–Z、假名、其他（#）。 */
export const artistInitialGroups = [..."ABCDEFGHIJKLMNOPQRSTUVWXYZ", "kana", "#"] as const;
export type ArtistInitialGroup = (typeof artistInitialGroups)[number];

/**
 * 角色的中文名（艺人页的分栏、列表的角色筛选、候选的理由）。发行艺人与专辑艺人对用户是同一回事（专辑这一级的艺人），
 * 都叫“专辑艺人”（I6）；接口的角色词表不变，合并只在显示层做。
 */
export const artistRoleLabels: Readonly<Record<ArtistRole, string>> = {
  release_artist: "专辑艺人",
  album_artist: "专辑艺人",
  track_artist: "曲目艺人",
  composer: "作曲",
  conductor: "指挥",
  performer: "演奏",
  producer: "制作人",
};

/** 首字母分组的显示：假名组写“假名”，其他写“#”。 */
export function initialGroupLabel(group: ArtistInitialGroup): string {
  return group === "kana" ? "假名" : group;
}

// album_count 是参与的可见专辑数（按组去重），track_count 是参与的当前存在的曲目数，两者都按全部角色统计；
// available 为 false 时这位艺人现在没有存在的曲目（身份与收藏保留）。
export const artistSummaryDecoder = d.object({
  id: d.uuid(),
  name: d.string(),
  initial_group: d.oneOf(artistInitialGroups),
  album_count: d.integer({ min: 0 }),
  track_count: d.integer({ min: 0 }),
  roles: d.array(d.oneOf(artistRoles), { max: artistRoles.length }),
  available: d.boolean(),
});

export type ArtistSummaryDTO = d.Infer<typeof artistSummaryDecoder>;

const countFormatter = new Intl.NumberFormat("zh-CN");

/** 艺人的概况：“3 张专辑 · 12 首曲目”；为 0 的一项不写，都为 0 时是空字符串。 */
export function artistMetaText(artist: Pick<ArtistSummaryDTO, "album_count" | "track_count">): string {
  return [
    artist.album_count === 0 ? null : `${countFormatter.format(artist.album_count)} 张专辑`,
    artist.track_count === 0 ? null : `${countFormatter.format(artist.track_count)} 首曲目`,
  ]
    .filter((part) => part !== null)
    .join(" · ");
}

/** 艺人参与的方式：“专辑艺人、作曲”，按固定的角色顺序；发行艺人与专辑艺人只写一次“专辑艺人”。 */
export function artistRolesText(roles: readonly ArtistRole[]): string {
  return [...new Set(artistRoles.filter((role) => roles.includes(role)).map((role) => artistRoleLabels[role]))].join("、");
}

/** 列表的角色筛选能选的角色：专辑这一级只有一个“专辑艺人”（role=album_artist，后端匹配发行艺人或专辑艺人）。 */
export const artistFilterRoles: readonly ArtistRole[] = artistRoles.filter((role) => role !== "release_artist");
