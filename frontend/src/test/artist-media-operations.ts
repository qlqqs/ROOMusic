import type { OperationChangesDTO, OperationItemDTO } from "../features/governance/api/decoders";
import { artistImage } from "./artist-media-data";

const artwork = {
  kind: "artist_external_artwork",
  source: "commons",
  external_id: "File:Portrait",
  artwork: { resource_id: artistImage.resource, mime: "image/png", width: 960, height: 640 },
} satisfies OperationChangesDTO;

const bio = {
  kind: "artist_external_bio",
  source: "wikipedia",
  external_id: "Test Artist",
  lang: "zh",
  length: 38,
} satisfies OperationChangesDTO;

export const artistMediaOperations = [artwork, bio].map((changes, i): OperationItemDTO => ({
  id: `00000000-0000-4000-8000-00000000010${i}`,
  type: changes.kind,
  created_at: "2026-11-24T10:00:00Z",
  actor: { id: "00000000-0000-4000-8000-000000000002", username: "curator" },
  artist: { id: "00000000-0000-4000-8000-000000000001", name: "测试艺人", visible: true, redirect_artist_id: null },
  album: null,
  release: null,
  changes,
  origin: { source: "curation", provider_kind: "source", provider: changes.source },
  reverts: null,
  reverted_by: null,
  revertible: true,
}));
