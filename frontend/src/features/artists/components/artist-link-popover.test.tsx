import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeArtistLinkPreview, type ArtistLinkPreviewDTO, type ArtistLinksDTO } from "../api/decoders";
import { ArtistLinkPopover, linkInputOf } from "./artist-link-popover";

const sample = decodeArtistLinkPreview(sampleBody("artists/link-preview"));
const artistId = "00000000-0000-4000-8000-000000000001";
const mbid = "abcd1234-0000-4000-8000-000000000002";

afterEach(() => vi.unstubAllGlobals());

function openPopover(response: Response, links: ArtistLinksDTO = { musicbrainz: null, wikidata: null, judged_by_model: false, operation_id: null }) {
  const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response);
  vi.stubGlobal("fetch", fetch);
  const onApply = vi.fn<(preview: ArtistLinkPreviewDTO) => Promise<boolean>>().mockResolvedValue(true);
  render(<ArtistLinkPopover artistId={artistId} initial={sample.external_id} links={links} anchorRef={{ current: null }} onApply={onApply} onClose={vi.fn()} />);
  return { fetch, onApply };
}

it.each([
  [`https://musicbrainz.org/artist/${mbid}`, "musicbrainz", mbid],
  [`https://beta.musicbrainz.org/artist/${mbid}/aliases?x=1`, "musicbrainz", mbid],
  [`  ${mbid.toUpperCase()}  `, "musicbrainz", mbid],
  ["https://www.wikidata.org/wiki/Q900001", "wikidata", "Q900001"],
  ["https://wikidata.org/entity/Q900001", "wikidata", "Q900001"],
  [" q900001 ", "wikidata", "Q900001"],
])("linkInputOf 识别并规范化 %s", (text, source, id) => {
  expect(linkInputOf(text)).toEqual({ source, id });
});

it.each(["", "普通文字", "Q0", "Q1234567890123", `https://musicbrainz.org/release/${mbid}`, "https://wikidata.org.example.test/wiki/Q42"])(
  "linkInputOf 拒绝非法输入 %j",
  (text) => expect(linkInputOf(text)).toBeNull(),
);

it.each([
  [true, "名字一致", "对应到这位"],
  [false, "名字与本地不一致", "名字不一致，仍然对应到这位"],
])("预览先显示骨架，再展示条目与名字匹配结果（%s）", async (name_matches, marker, confirm) => {
  const user = userEvent.setup();
  const preview = { ...sample, name_matches };
  const { fetch, onApply } = openPopover(Response.json(preview));
  const loading = screen.getByLabelText("正在读取条目");
  expect(loading.getAttribute("aria-busy")).toBe("true");
  expect(screen.queryByRole("button", { name: confirm })).toBeNull();

  expect(await screen.findByText(preview.name)).toBeTruthy();
  expect(screen.queryByLabelText("正在读取条目")).toBeNull();
  expect(screen.getByText(preview.description ?? "")).toBeTruthy();
  expect(screen.getByText(`${preview.wikidata} · ${preview.musicbrainz?.slice(0, 8)}`)).toBeTruthy();
  expect(screen.getByLabelText(marker)).toBeTruthy();
  expect(fetch).toHaveBeenCalledWith(`/api/v1/artists/${artistId}/link-preview`, expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ url: preview.external_id }),
  }));
  await user.click(screen.getByRole("button", { name: confirm }));
  expect(onApply).toHaveBeenCalledExactlyOnceWith(preview);
});

it("与当前对应相同时显示已对应并隐藏确认按钮", async () => {
  const { onApply } = openPopover(Response.json(sample), { musicbrainz: sample.musicbrainz, wikidata: sample.wikidata, judged_by_model: false, operation_id: null });
  expect(await screen.findByLabelText("已是当前对应")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /对应到这位/ })).toBeNull();
  expect(onApply).not.toHaveBeenCalled();
});

it("仅 Wikidata 已匹配时，仍能确认更换 MusicBrainz 对应", async () => {
  const preview = { ...sample, name_matches: true };
  openPopover(Response.json(preview), { musicbrainz: mbid, wikidata: preview.wikidata, judged_by_model: false, operation_id: null });
  await screen.findByText(preview.name);
  expect(screen.queryByLabelText("已是当前对应")).toBeNull();
  expect(screen.queryByRole("button", { name: "对应到这位" })).not.toBeNull();
});

it.each([
  [404, "not_found", "找不到这个条目。"],
  [400, "invalid_input", "无法识别这个链接。"],
  [409, "source_inactive", "这个来源没有启用，请先在“自动整理”的来源里打开。"],
])("后端报 %s %s 时显示对应错误", async (status, code, message) => {
  openPopover(Response.json({ error: { code, message: code }, request_id: "test-link-preview" }, { status }));
  expect((await screen.findByRole("alert")).textContent).toBe(message);
  expect(screen.queryByRole("button", { name: /对应到这位/ })).toBeNull();
});
