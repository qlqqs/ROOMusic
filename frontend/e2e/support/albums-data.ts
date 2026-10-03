import type { ReleaseGroupSummaryDTO } from "../../src/features/catalog/api/decoders";
import { albumGroup, groupDetailOf, mediaOf, releaseDetailOf, releaseVersions } from "../../src/test/catalog-data";
import type { ApiMock } from "./api-mock";

// 专辑页面的 e2e 数据：多版本多介质的专辑，以及按 A7 要求造的长内容专辑。
// 只依赖纯模块（catalog-data）与 ApiMock 的类型：e2e 运行在 Node 里，不能导入会连带导入 CSS 的应用代码。

const mixed = "第九交響曲「合唱」ニ短調 作品125 · Symphonie Nr. 9 d-Moll, Op. 125 “Choral” · 贝多芬第九交响曲《欢乐颂》";

/** 超过 1000 个字符的合法标题：中日英混排，带空格与标点，可以在词与词之间换行。 */
export const LONG_TITLE = Array.from({ length: 12 }, (_, index) => `${mixed} 第${index + 1}部`).join(" — ");

/** 很长的艺人名：中日英混排，三种署名写法接在一起。 */
export const LONG_ARTIST =
  "ヘルベルト・フォン・カラヤン指揮ベルリン・フィルハーモニー管弦楽団、ウィーン楽友協会合唱団 ‖ Herbert von Karajan conducting the Berliner Philharmoniker and the Wiener Singverein ‖ 赫伯特·冯·卡拉扬指挥柏林爱乐乐团与维也纳音乐之友协会合唱团";

/** 没有任何空格的长串（目录编号、网址之类）：只能在字符之间折行。 */
export const LONG_TOKEN = "Supercalifragilisticexpialidocious".repeat(10);

/** 一个单版本、双碟、三首曲目、有封面的专辑组：详情页的基本形态。 */
export function singleVersionGroup(index = 1): ReleaseGroupSummaryDTO {
  return albumGroup(index, {
    title: "Sample Symphonies",
    album_artist: "Sample Orchestra",
    artist: "Sample Orchestra",
    year: 1998,
    medium_count: 2,
    track_count: 3,
    artwork: true,
  });
}

/** 一个多版本、多介质的专辑组：三个版本（年份依次晚十二年），两张介质共十二首曲目，有封面，需要关注。 */
export function multiVersionGroup(index = 1): ReleaseGroupSummaryDTO {
  return albumGroup(index, {
    title: "Symphony No. 9 in D minor, Op. 125 “Choral”",
    album_artist: "Berliner Philharmoniker · Herbert von Karajan",
    artist: "Berliner Philharmoniker",
    year: 1977,
    medium_count: 2,
    track_count: 12,
    release_count: 3,
    attention_count: 1,
    artwork: true,
  });
}

/** 为组登记三个版本的组详情；返回各版本的摘要，第一个是代表版本。 */
export function seedVersions(mock: ApiMock, group: ReleaseGroupSummaryDTO, count = 3) {
  const versions = releaseVersions(group, count, (version) => ({ year: 1977 + (version - 1) * 12 }));
  mock.groupDetails.set(group.id, groupDetailOf(group, versions));
  return versions;
}

/** 长内容的专辑组：标题超过 1000 字，专辑艺人很长，艺人是无空格长串，十二张介质共 36 首曲目。 */
export function longContentGroup(index = 90): ReleaseGroupSummaryDTO {
  return albumGroup(index, {
    title: LONG_TITLE,
    album_artist: LONG_ARTIST,
    artist: LONG_TOKEN,
    year: 2001,
    medium_count: 12,
    track_count: 36,
    attention_count: 1,
    artwork: true,
  });
}

/** 登记长内容专辑的版本详情：曲目标题、艺人与署名、厂牌与编号里都有长文字与无空格长串。 */
export function seedLongContent(mock: ApiMock, group: ReleaseGroupSummaryDTO): void {
  mock.releases.set(
    group.representative.id,
    releaseDetailOf(group.representative, {
      label: LONG_ARTIST,
      catalog_number: LONG_TOKEN,
      edition: LONG_TITLE.slice(0, 300),
      source_type: "CD",
      media_type: "CD",
      credits: [
        { role: "album_artist", name: LONG_ARTIST },
        { role: "composer", name: LONG_TOKEN },
      ],
      media: mediaOf(group.representative, (_medium, track) => ({
        title: track === 1 ? LONG_TITLE.slice(0, 500) : track === 2 ? LONG_TOKEN : mixed,
        artist: track === 1 ? LONG_ARTIST : LONG_TOKEN,
        credits: [
          { role: "composer", name: LONG_TOKEN },
          { role: "conductor", name: LONG_ARTIST },
        ],
      })),
    }),
  );
}
