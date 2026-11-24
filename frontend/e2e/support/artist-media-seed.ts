import { artistBio, artistImage } from "../../src/test/artist-media-data";
import type { ApiMock } from "./api-mock";
import { seedArtistsE2E } from "./artist-seed";

const longBio = [
  "青岚室内乐团在河畔小城成立，成员来自不同的音乐传统。乐团以木管、弦乐和人声为基础，为地方民谣编配新的声部，也定期举办小型室内音乐会。",
  "创团初期，成员在图书馆排练，从同一支旋律出发尝试不同的节拍与配器。后来，乐团把这些练习整理成一套曲目，在旧剧院、社区礼堂和学校轮流演出。",
  "每年秋天的演出季都包括一场开放排练。听众可以在休息时和演奏者交流，近距离听到不同乐器的音色，以及同一段旋律在不同空间里的变化。",
  "近年的录音保留了现场的呼吸与停顿。乐团邀请本地作曲家为小编制创作新作品，也把早期演出留下的曲谱逐页整理归档。",
  "乐团的下一套曲目以水边生活为主题，串起港口号子、摇篮曲和原创作品。为使第一次到场的观众也能轻松进入音乐，演出开始前由成员讲述每首作品的来历。",
].join("\n");

export function seedArtistMedia(mock: ApiMock, scenario: "with-media" | "without-media" | "broken-image") {
  const seed = seedArtistsE2E(mock);
  const detail = seed.data.details.get(seed.artist.id);
  if (detail === undefined) throw new Error("缺少艺人详情样本");
  detail.artist.name = "青岚室内乐团";
  detail.artist.image = scenario === "without-media" ? null : artistImage.resource;
  detail.image = scenario === "without-media" ? null : artistImage;
  detail.bio = scenario === "with-media" ? { ...artistBio, text: longBio } : null;
  if (scenario === "broken-image") mock.brokenArtworks.add(artistImage.resource);
  return detail.artist;
}
