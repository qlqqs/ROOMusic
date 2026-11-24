import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { artistBio, artistImage } from "../../../test/artist-media-data";
import { ArtistBio } from "./artist-bio";

it("超过可见高度时可以展开简介再收起", async () => {
  const scrollHeight = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(160);
  const clientHeight = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(80);
  try {
    const user = userEvent.setup();
    render(<ArtistBio bio={{ ...artistBio, text: artistBio.text.repeat(30) }} image={null} />);
    const expand = screen.getByRole("button", { name: "展开" });
    expect(expand.getAttribute("aria-expanded")).toBe("false");
    await user.click(expand);
    const collapse = screen.getByRole("button", { name: "收起" });
    expect(collapse.getAttribute("aria-expanded")).toBe("true");
    await user.click(collapse);
    expect(screen.getByRole("button", { name: "展开" }).getAttribute("aria-expanded")).toBe("false");
  } finally {
    scrollHeight.mockRestore();
    clientHeight.mockRestore();
  }
});

it("没有简介时只显示图片署名，两者都没有时不渲染", () => {
  const { container, rerender } = render(<ArtistBio bio={null} image={artistImage} />);
  const credits = screen.getByRole("region", { name: "艺人简介" });
  expect(credits.textContent).toBe("图片：Wikimedia Commons · 合成样本作者 · CC BY 4.0");
  expect(within(credits).queryByRole("button")).toBeNull();

  rerender(<ArtistBio bio={null} image={null} />);
  expect(container.firstChild).toBeNull();
});

it("署名只为 HTTP(S) 地址创建外链，其他地址对应的署名保留为文本", () => {
  render(<ArtistBio
    bio={{ ...artistBio, license_url: "javascript:void(0)" }}
    image={{ ...artistImage, source_url: "//commons.example.test/wiki/File:Portrait" }}
  />);
  const credits = within(screen.getByRole("region", { name: "艺人简介" }));
  for (const [name, address] of [["维基百科", artistBio.source_url], ["CC BY 4.0", artistImage.license_url]] as const) {
    const link = credits.getByRole("link", { name });
    expect(link.getAttribute("href")).toBe(address);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")?.split(/\s+/)).toEqual(expect.arrayContaining(["noopener", "noreferrer"]));
  }
  expect(credits.getAllByRole("link")).toHaveLength(2);
  expect(credits.getByText("CC BY-SA 4.0", { exact: false }).textContent).toContain("CC BY-SA 4.0");
  expect(credits.getByText("Wikimedia Commons", { exact: false }).textContent).toContain("Wikimedia Commons");
});
