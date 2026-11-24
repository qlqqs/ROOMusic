import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeArtistDetail, type ArtistTrackRole } from "../api/decoders";
import { ArtistHeader } from "./artist-header";

const artist = decodeArtistDetail(sampleBody("artists/detail")).artist;
const path = `/artists/${artist.id}`;

function renderHeader(roles: readonly ArtistTrackRole[]) {
  const root = createRootRoute({
    component: () => <ArtistHeader artist={artist} actions={null} compactTitle={false} trackRoles={roles} />,
  });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: [`${path}?tracks_page=2`] }),
  });
  render(<RouterProvider router={router} />);
}

it("页头角色仅列出创作与幕后角色，链接跳到相应曲目并重置页码", async () => {
  const roles = {
    lyricist: "作词",
    composer: "作曲",
    arranger: "编曲",
    conductor: "指挥",
    performer: "演奏",
    producer: "制作人",
  } satisfies Partial<Record<ArtistTrackRole, string>>;
  renderHeader(["track_artist", "lyricist", "composer", "arranger", "conductor", "performer", "producer"]);
  const row = within(await screen.findByRole("list", { name: "角色" }));
  expect(row.getAllByRole("link").map((link) => link.textContent)).toEqual(Object.values(roles));
  for (const [role, label] of Object.entries(roles)) {
    expect(row.getByRole("link", { name: label }).getAttribute("href")).toBe(`${path}?track_role=${role}#artist-tracks`);
  }
});

it("只有专辑与曲目艺人角色时不渲染角色行", async () => {
  renderHeader(["track_artist"]);
  expect(await screen.findByRole("heading", { name: artist.name })).toBeTruthy();
  expect(screen.queryByRole("list", { name: "角色" })).toBeNull();
});
