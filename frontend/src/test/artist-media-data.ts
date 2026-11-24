import type { ArtistBioDTO, ArtistImageDTO } from "../features/artists/api/decoders";

export const artistImage = {
  resource: "m8-artist-portrait",
  source: "commons",
  source_url: "https://commons.example.test/wiki/File:Portrait",
  author: "合成样本作者",
  license: "CC BY 4.0",
  license_url: "https://creativecommons.example.test/licenses/by/4.0/",
  citations: [],
} satisfies ArtistImageDTO;

export const artistBio = {
  text: "这是一段用于测试的艺人简介。",
  lang: "zh",
  source: "wikipedia",
  source_url: "http://wiki.example.test/Artist",
  license: "CC BY-SA 4.0",
  license_url: "https://creativecommons.example.test/licenses/by-sa/4.0/",
  citations: [],
} satisfies ArtistBioDTO;
