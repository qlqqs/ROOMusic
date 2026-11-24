import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ArtistMonogram } from "./artist-monogram";

it("有图片时使用同源资源，加载失败显示缩写，换图后重新加载", () => {
  const artist = { id: "sample-artist", name: "Leonard Bernstein" };
  const { rerender } = render(<ArtistMonogram {...artist} image="portrait-first" />);
  const first = screen.getByAltText("");
  expect(first.tagName).toBe("IMG");
  expect(first.getAttribute("src")).toBe("/api/v1/artworks/portrait-first");
  expect(screen.queryByText("LB")).toBeNull();

  fireEvent.error(first);
  expect(screen.queryByAltText("")).toBeNull();
  expect(screen.getByText("LB")).toBeTruthy();

  rerender(<ArtistMonogram {...artist} image="portrait-next" />);
  expect(screen.getByAltText("").getAttribute("src")).toBe("/api/v1/artworks/portrait-next");
  expect(screen.queryByText("LB")).toBeNull();
});
