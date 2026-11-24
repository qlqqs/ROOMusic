import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { citationLabelOf } from "./artist-media-source";
import { CitationRows } from "./citation-rows";

it.each([
  ["https://www.example.test/", "example.test"],
  ["http://archive.example.test/%E6%BC%94%E5%87%BA?page=1#music", "archive.example.test/演出"],
  ["javascript:void(0)", null],
  ["ftp://example.test/artist", null],
  ["mailto:artist@example.test", null],
  ["//example.test/artist", null],
  ["无效地址", null],
])("简写引用地址 %s", (url, label) => {
  expect(citationLabelOf(url)).toBe(label);
});

it("只展示 HTTP(S) 引用，保留完整地址并用标题作悬停提示", () => {
  const secure = { url: "https://www.example.test/artist?v=2#bio", title: "演出档案" };
  const plain = { url: "http://archive.example.test/profile", title: "" };
  render(<CitationRows citations={[
    secure,
    plain,
    ...["javascript:void(0)", "ftp://example.test/artist", "//example.test/artist"].map((url) => ({ url, title: "不可点击的引用" })),
  ]} />);

  const archive = screen.getByRole<HTMLAnchorElement>("link", { name: "example.test/artist" });
  const profile = screen.getByRole<HTMLAnchorElement>("link", { name: "archive.example.test/profile" });
  expect(screen.getAllByRole("link")).toEqual([archive, profile]);
  expect(screen.queryByTitle("不可点击的引用")).toBeNull();
  expect(archive.getAttribute("href")).toBe(secure.url);
  expect(archive.getAttribute("title")).toBe(secure.title);
  expect(profile.getAttribute("href")).toBe(plain.url);
  expect(profile.getAttribute("title")).toBe("archive.example.test/profile");
  for (const link of [archive, profile]) {
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.relList.contains("noopener")).toBe(true);
    expect(link.relList.contains("noreferrer")).toBe(true);
  }
});
