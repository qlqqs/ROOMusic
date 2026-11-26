import { vi } from "vitest";
import type { ArtistMediaControls } from "../features/artists/hooks/use-artist-media";

export function artistMediaControls() {
  return {
    upload: { status: "idle" },
    uploadImage: vi.fn<ArtistMediaControls["uploadImage"]>(),
    saveBio: vi.fn<ArtistMediaControls["saveBio"]>().mockResolvedValue(true),
    savingBio: false,
    reject: vi.fn<ArtistMediaControls["reject"]>(),
    undoReject: vi.fn(),
    pendingReject: null,
    hidden: { image: false, bio: false },
    applyLink: vi.fn<ArtistMediaControls["applyLink"]>().mockResolvedValue(true),
    error: null,
    webSearch: null,
    decideCandidates: vi.fn(() => Promise.resolve(true)),
    deciding: false,
    startWebSearch: vi.fn(() => Promise.resolve(true)),
    startingWebSearch: false,
    dismissError: vi.fn(),
  } satisfies ArtistMediaControls;
}
