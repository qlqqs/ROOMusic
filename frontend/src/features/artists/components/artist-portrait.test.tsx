import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { artistMediaControls } from "../../../test/artist-media-controls";
import { artistImage } from "../../../test/artist-media-data";
import { decodeArtistDetail, type ArtistImageDTO } from "../api/decoders";
import { ArtistPortrait } from "./artist-portrait";

const mbid = "00000000-0000-4000-8000-000000000002";
const links = { musicbrainz: mbid, wikidata: "Q900001" };
const artist = { id: "00000000-0000-4000-8000-000000000001", name: "青岚室内乐团", image: artistImage.resource };
const manualDetail = decodeArtistDetail(sampleBody("artists/detail-manual-media"));

afterEach(() => vi.unstubAllGlobals());

it.each<{ image: ArtistImageDTO | null; sourceName: string; kind: string; glyph: string }>([
  { image: artistImage, sourceName: "Wikimedia Commons · CC BY 4.0", kind: "licensed", glyph: "book-open" },
  { image: { ...artistImage, source: "plugin:synthetic-media", license: null }, sourceName: "插件 synthetic-media", kind: "plugin", glyph: "puzzle" },
  { image: manualDetail.image, sourceName: "管理员提供", kind: "manual", glyph: "user-round-pen" },
])("显示 $sourceName 的印章、图标与悬停提示", async ({ image, sourceName, kind, glyph }) => {
  const user = userEvent.setup();
  render(<ArtistPortrait artist={artist} image={image} links={links} media={null} />);
  const label = `图片来源：${sourceName}`;
  const seal = screen.getByRole("button", { name: label });
  expect(seal.getAttribute("data-kind")).toBe(kind);
  expect(seal.querySelector(`svg.lucide-${glyph}[aria-hidden="true"]`)).not.toBeNull();
  await user.hover(seal);
  expect((await screen.findByText(label)).getAttribute("aria-hidden")).toBe("true");
});

it("来源弹层显示作者、许可和外部对应，普通成员没有写入入口", async () => {
  const user = userEvent.setup();
  const { container } = render(<ArtistPortrait artist={artist} image={artistImage} links={links} media={null} />);
  const seal = screen.getByRole("button", { name: "图片来源：Wikimedia Commons · CC BY 4.0" });
  await user.click(seal);

  const dialog = within(screen.getByRole("dialog", { name: "图片来源" }));
  expect(dialog.getByText("合成样本作者")).toBeTruthy();
  for (const [name, href] of [
    ["Wikimedia Commons", artistImage.source_url],
    ["CC BY 4.0", artistImage.license_url],
    ["MusicBrainz", `https://musicbrainz.org/artist/${mbid}`],
    ["Wikidata Q900001", "https://www.wikidata.org/wiki/Q900001"],
  ] as const) {
    const link = dialog.getByRole("link", { name });
    expect(link.getAttribute("href")).toBe(href);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")?.split(/\s+/)).toEqual(expect.arrayContaining(["noopener", "noreferrer"]));
  }
  expect(dialog.queryAllByRole("button")).toHaveLength(0);
  expect(screen.getAllByRole("button")).toEqual([seal]);
  expect(container.querySelector('input[type="file"]')).toBeNull();

  expect(document.activeElement).toBe(dialog.getByRole("link", { name: "Wikimedia Commons" }));
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(seal);
});

it("非 HTTP(S) 的来源与许可地址保留为文本", async () => {
  const user = userEvent.setup();
  render(
    <ArtistPortrait
      artist={artist}
      image={{ ...artistImage, source_url: "javascript:void(0)", license_url: "//example.test/license" }}
      links={{ musicbrainz: null, wikidata: null }}
      media={null}
    />,
  );
  await user.click(screen.getByRole("button", { name: /^图片来源/ }));
  const dialog = within(screen.getByRole("dialog", { name: "图片来源" }));
  expect(dialog.getByText("Wikimedia Commons")).toBeTruthy();
  expect(dialog.getByText("CC BY 4.0")).toBeTruthy();
  expect(dialog.queryAllByRole("link")).toHaveLength(0);
});

it("无图时只有管理员看到“指定对应”印章，打开后聚焦输入框", async () => {
  const user = userEvent.setup();
  const withoutImage = { ...artist, image: null };
  const { rerender } = render(<ArtistPortrait artist={withoutImage} image={null} links={links} media={null} />);
  expect(screen.queryByRole("button")).toBeNull();

  rerender(<ArtistPortrait artist={withoutImage} image={null} links={links} media={artistMediaControls()} />);
  const seal = screen.getByRole("button", { name: "指定对应" });
  expect(seal.getAttribute("data-kind")).toBe("link");
  expect(seal.querySelector("svg.lucide-link-2")).not.toBeNull();
  await user.click(seal);
  const dialog = within(screen.getByRole("dialog", { name: "指定对应" }));
  const input = dialog.getByRole("textbox", { name: "MusicBrainz 或 Wikidata 链接" });
  expect(document.activeElement).toBe(input);
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(seal);
});

it("只在可编辑区域外粘贴可识别内容时打开指定对应", async () => {
  const user = userEvent.setup();
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(Response.json(sampleBody("artists/link-preview")));
  vi.stubGlobal("fetch", fetch);
  render(<><input aria-label="页面其他输入框" /><ArtistPortrait artist={artist} image={artistImage} links={links} media={artistMediaControls()} /></>);
  const input = screen.getByRole<HTMLInputElement>("textbox", { name: "页面其他输入框" });
  const url = "https://www.wikidata.org/wiki/Q42";

  await user.click(input);
  await user.paste(url);
  expect(input.value).toBe(url);
  expect(screen.queryByRole("dialog")).toBeNull();
  await user.click(document.body);
  await user.paste("普通文字");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(fetch).not.toHaveBeenCalled();

  await user.paste(url);
  const dialog = within(screen.getByRole("dialog", { name: "指定对应" }));
  const pasted = dialog.getByRole<HTMLInputElement>("textbox");
  expect(pasted.value).toBe(url);
  expect(document.activeElement).toBe(pasted);
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("管理员拖入图片时提示可放置，放下后交给上传", () => {
  const media = artistMediaControls();
  const { container } = render(<ArtistPortrait artist={artist} image={artistImage} links={links} media={media} />);
  const portrait = container.firstElementChild;
  if (portrait === null) throw new Error("缺少艺人圆章");
  const file = new File(["合成图片"], "portrait.png", { type: "image/png" });
  const dataTransfer = { files: [file], types: ["Files"], dropEffect: "none" };

  expect(fireEvent.dragOver(portrait, { dataTransfer })).toBe(false);
  expect(portrait.hasAttribute("data-drop")).toBe(true);
  expect(dataTransfer.dropEffect).toBe("copy");
  expect(media.uploadImage).not.toHaveBeenCalled();
  fireEvent.drop(portrait, { dataTransfer });
  expect(portrait.hasAttribute("data-drop")).toBe(false);
  expect(media.uploadImage).toHaveBeenCalledExactlyOnceWith(file);
});
