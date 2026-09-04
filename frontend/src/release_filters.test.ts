/* global URL */
import { describe, expect, it } from "vitest";
import { clampReleasePage, readReleaseFilters, releaseFilterURL } from "./release_filters";

describe("release URL filters", () => {
  it("reads only the allowlisted attention value", () => {
    expect(readReleaseFilters("?q=%20Album%20&attention=required&page=3")).toEqual({ query: "Album", attentionRequired: true, page: 3, release: null });
    expect(readReleaseFilters("?attention=guessed&page=-1")).toEqual({ query: "", attentionRequired: false, page: 1, release: null });
    expect(readReleaseFilters("?page=1.5")).toEqual({ query: "", attentionRequired: false, page: 1, release: null });
  });

  it("updates query and attention while preserving unrelated URL state", () => {
    const filtered = new URL(releaseFilterURL("https://roomusic.test/library?tab=albums", { query: " Miles ", attentionRequired: true, page: 4, release: null }));
    expect(filtered.searchParams.get("q")).toBe("Miles");
    expect(filtered.searchParams.get("attention")).toBe("required");
    expect(filtered.searchParams.get("page")).toBe("4");
    expect(filtered.searchParams.get("tab")).toBe("albums");

    const cleared = new URL(releaseFilterURL(filtered.toString(), { query: "", attentionRequired: false, page: 1, release: null }));
    expect(cleared.searchParams.has("q")).toBe(false);
    expect(cleared.searchParams.has("attention")).toBe(false);
    expect(cleared.searchParams.has("page")).toBe(false);
  });

  it("round-trips the optional release selection and falls back on malformed values", () => {
    expect(readReleaseFilters("?release=release-abc123")).toEqual({ query: "", attentionRequired: false, page: 1, release: "release-abc123" });
    expect(readReleaseFilters("?release=%20%20")).toEqual({ query: "", attentionRequired: false, page: 1, release: null });
    expect(readReleaseFilters("?release=")).toEqual({ query: "", attentionRequired: false, page: 1, release: null });

    const selected = new URL(releaseFilterURL("https://roomusic.test/library?q=Miles&page=2", { query: "Miles", attentionRequired: false, page: 2, release: " release-9 " }));
    expect(selected.searchParams.get("release")).toBe("release-9");
    expect(selected.searchParams.get("q")).toBe("Miles");
    expect(selected.searchParams.get("page")).toBe("2");

    const deselected = new URL(releaseFilterURL(selected.toString(), { query: "Miles", attentionRequired: false, page: 2, release: null }));
    expect(deselected.searchParams.has("release")).toBe(false);
    expect(deselected.searchParams.get("q")).toBe("Miles");
  });

  it("clamps an out-of-range page to the last real result page", () => {
    expect(clampReleasePage(999, 0, 50)).toBe(1);
    expect(clampReleasePage(999, 101, 50)).toBe(3);
    expect(clampReleasePage(2, Number.MAX_SAFE_INTEGER, 100)).toBe(2);
    expect(clampReleasePage(Number.MAX_SAFE_INTEGER + 1, 100, 50)).toBe(1);
    expect(clampReleasePage(2, -1, 50)).toBe(1);
    expect(clampReleasePage(2, 100, 0)).toBe(1);
  });
});
