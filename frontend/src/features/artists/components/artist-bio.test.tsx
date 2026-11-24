import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { artistMediaControls } from "../../../test/artist-media-controls";
import { artistBio } from "../../../test/artist-media-data";
import { ArtistBio } from "./artist-bio";

it("正文溢出时，图标行提供来源与展开按钮，展开后可收起", async () => {
  const scrollHeight = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(160);
  const clientHeight = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(72);
  try {
    const user = userEvent.setup();
    const text = artistBio.text.repeat(30);
    render(<ArtistBio bio={{ ...artistBio, text }} media={null} />);
    const bio = within(screen.getByRole("region", { name: "艺人简介" }));
    const paragraph = bio.getByText(text);
    expect(paragraph.getAttribute("lang")).toBe("zh-CN");
    const source = bio.getByRole("button", { name: "简介来源：维基百科 · CC BY-SA 4.0" });
    const expand = bio.getByRole("button", { name: "展开" });
    expect(bio.getAllByRole("button")).toEqual([source, expand]);
    expect(bio.queryAllByRole("link")).toHaveLength(0);
    expect(bio.queryByText(/简介：|图片：/)).toBeNull();
    expect(paragraph.hasAttribute("data-expanded")).toBe(false);
    expect(expand.getAttribute("aria-expanded")).toBe("false");

    await user.click(expand);
    const collapse = bio.getByRole("button", { name: "收起" });
    expect(collapse.getAttribute("aria-expanded")).toBe("true");
    expect(paragraph.hasAttribute("data-expanded")).toBe(true);
    await user.click(collapse);
    expect(bio.getByRole("button", { name: "展开" }).getAttribute("aria-expanded")).toBe("false");
    expect(paragraph.hasAttribute("data-expanded")).toBe(false);
  } finally {
    scrollHeight.mockRestore();
    clientHeight.mockRestore();
  }
});

it("简介来源弹层显示来源及许可，并在 Esc 后把焦点还给图标", async () => {
  const user = userEvent.setup();
  render(<ArtistBio bio={{ ...artistBio, license_url: "javascript:void(0)" }} media={null} />);
  const source = screen.getByRole("button", { name: "简介来源：维基百科 · CC BY-SA 4.0" });
  await user.click(source);
  const dialog = within(screen.getByRole("dialog", { name: "简介来源" }));
  const link = dialog.getByRole("link", { name: "维基百科" });
  expect(link.getAttribute("href")).toBe(artistBio.source_url);
  expect(link.getAttribute("target")).toBe("_blank");
  expect(dialog.getAllByRole("link")).toEqual([link]);
  expect(dialog.getByText("CC BY-SA 4.0")).toBeTruthy();
  expect(dialog.queryAllByRole("button")).toHaveLength(0);
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(source);
});

it("管理员原地编辑，Esc 取消，Ctrl+Enter 保存去除首尾空白的简介", async () => {
  const user = userEvent.setup();
  const media = artistMediaControls();
  render(<ArtistBio bio={artistBio} media={media} />);
  expect(screen.getByRole("button", { name: "简介不对" })).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "编辑简介" }));
  const editor = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "简介" });
  expect(editor.value).toBe(artistBio.text);
  expect(document.activeElement).toBe(editor);
  await user.clear(editor);
  await user.type(editor, "草稿");
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.getByText(artistBio.text)).toBeTruthy();
  expect(media.saveBio).not.toHaveBeenCalled();

  await user.click(screen.getByRole("button", { name: "编辑简介" }));
  const reopened = screen.getByRole("textbox", { name: "简介" });
  fireEvent.change(reopened, { target: { value: "  𠮷🎵a  " } });
  expect(screen.getByText("3 / 4000")).toBeTruthy();
  await user.keyboard("{Control>}{Enter}{/Control}");
  expect(media.saveBio).toHaveBeenCalledExactlyOnceWith("𠮷🎵a");
  await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
});

it("超过 4000 个码点时鼠标和快捷键都不能提交，4000 个可以保存", async () => {
  const user = userEvent.setup();
  const media = artistMediaControls();
  render(<ArtistBio bio={artistBio} media={media} />);
  await user.click(screen.getByRole("button", { name: "编辑简介" }));
  const editor = screen.getByRole("textbox", { name: "简介" });
  const limit = "🎵".repeat(4000);
  fireEvent.change(editor, { target: { value: `${limit}𠮷` } });
  expect(screen.getByText("4001 / 4000")).toBeTruthy();
  await user.keyboard("{Control>}{Enter}{/Control}");
  await user.click(screen.getByRole("button", { name: "保存" }));
  expect(media.saveBio).not.toHaveBeenCalled();

  fireEvent.change(editor, { target: { value: limit } });
  expect(screen.getByText("4000 / 4000")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "保存" }));
  expect(media.saveBio).toHaveBeenCalledExactlyOnceWith(limit);
});

it("没有简介时，只有管理员能打开空白的简介编辑器", async () => {
  const user = userEvent.setup();
  const { rerender } = render(<ArtistBio bio={null} media={null} />);
  expect(screen.queryByRole("region", { name: "艺人简介" })).toBeNull();
  expect(screen.queryByRole("button", { name: "写简介" })).toBeNull();

  rerender(<ArtistBio bio={null} media={artistMediaControls()} />);
  await user.click(screen.getByRole("button", { name: "写简介" }));
  expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "简介" }).value).toBe("");
  expect(screen.getByText("0 / 4000")).toBeTruthy();
});
