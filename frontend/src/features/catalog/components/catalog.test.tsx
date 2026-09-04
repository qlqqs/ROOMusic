// @vitest-environment jsdom
/* global MouseEvent, KeyboardEvent, Event, HTMLDivElement, Element */
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReleaseDetailDTO, TrackDTO } from "../../../api";
import { MediumSection } from "./medium-section";
import { ReleaseCard } from "./release-card";
import { ReleaseCover } from "./release-cover";
import { ReleaseDetailDrawer } from "./release-detail-drawer";
import { toReleaseCardModel } from "../model/display";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

function makeTrack(id: string, overrides: Partial<TrackDTO> = {}): TrackDTO {
  return {
    id,
    title: `曲目 ${id}`,
    artist: "艺术家",
    position: 1,
    source: `${id}.flac`,
    source_kind: "flac_vorbis_comment",
    duration_seconds: null,
    codec: "flac",
    bit_depth: null,
    sample_rate: null,
    channels: null,
    bitrate: null,
    cue: null,
    credits: [],
    ...overrides,
  };
}

function makeDetail(): ReleaseDetailDTO {
  return {
    id: "release-1",
    title: "专辑",
    artist: "艺术家",
    album_artist: null,
    year: 2026,
    source_type: null,
    media_type: "CD",
    medium_count: 1,
    track_count: 2,
    attention_count: 0,
    artwork: { resource_id: "cover-1", mime: "image/png", width: 300, height: 300 },
    candidate_kind: "ordinary_directory",
    genre: null,
    catalog_number: null,
    edition: null,
    label: null,
    barcode: null,
    credits: [],
    evidence: [],
    media: [{ id: "medium-1", position: 1, title: "", tracks: [makeTrack("t1"), makeTrack("t2")] }],
  };
}

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
let mounted = false;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  mounted = true;
});

afterEach(() => {
  if (mounted) act(() => root.unmount());
  container.remove();
});

function render(ui: ReactElement) {
  act(() => root.render(ui));
}

function click(element: Element) {
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

describe("ReleaseCover", () => {
  it("shows the deterministic fallback when artwork is absent", () => {
    render(<ReleaseCover artwork={null} title="爵士之夜" />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".release-cover")?.getAttribute("data-cover-status")).toBe("absent");
    expect(container.textContent).toContain("爵");
  });

  it("builds the authorized URL, tracks ready state, and recovers from broken", () => {
    render(<ReleaseCover artwork={{ resource_id: "cover a", mime: "image/png", width: 1, height: 1 }} title="专辑" allowRetry />);
    const image = container.querySelector("img");
    expect(image?.getAttribute("src")).toBe("/api/v1/artworks/cover%20a");
    expect(image?.getAttribute("alt")).toBe("专辑 封面");
    expect(container.querySelector(".release-cover")?.getAttribute("data-cover-status")).toBe("loading");

    act(() => { image?.dispatchEvent(new Event("load")); });
    expect(container.querySelector(".release-cover")?.getAttribute("data-cover-status")).toBe("ready");

    act(() => { container.querySelector("img")?.dispatchEvent(new Event("error")); });
    expect(container.querySelector(".release-cover")?.getAttribute("data-cover-status")).toBe("broken");
    const retry = container.querySelector(".cover-retry");
    expect(retry?.textContent).toBe("重试");
    if (!retry) throw new Error("retry button missing");
    click(retry);
    expect(container.querySelector(".release-cover")?.getAttribute("data-cover-status")).toBe("loading");
    expect(container.querySelector("img")).not.toBeNull();
  });
});

describe("ReleaseCard", () => {
  it("emits open intent and renders attention badge without nested buttons", () => {
    const onOpen = vi.fn();
    const model = toReleaseCardModel({
      id: "release-9",
      title: "专辑",
      artist: "",
      album_artist: null,
      year: null,
      source_type: null,
      media_type: null,
      medium_count: 1,
      track_count: 3,
      attention_count: 2,
      artwork: null,
    });
    render(<ReleaseCard model={{ ...model, title: "专辑" }} layout="grid" onOpen={onOpen} />);
    const card = container.querySelector("button.release-card");
    expect(card?.getAttribute("aria-label")).toBe("查看 专辑 详情");
    expect(container.textContent).toContain("需要检查 2");
    expect(card?.querySelectorAll("button")).toHaveLength(0);
    if (!card) throw new Error("card missing");
    click(card);
    expect(onOpen).toHaveBeenCalledWith("release-9");
  });
});

describe("MediumSection", () => {
  it("toggles the disclosure region and emits play intent", () => {
    const onToggle = vi.fn();
    const onPlayTrack = vi.fn();
    const medium = { id: "medium-1", position: 1, title: "碟一", tracks: [makeTrack("t1")] };
    const { rerender } = (() => {
      render(<MediumSection medium={medium} expanded onToggle={onToggle} onPlayTrack={onPlayTrack} />);
      return { rerender: (expanded: boolean) => act(() => root.render(<MediumSection medium={medium} expanded={expanded} onToggle={onToggle} onPlayTrack={onPlayTrack} />)) };
    })();
    const toggle = container.querySelector(".medium-toggle");
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    const region = container.querySelector("[role='region']");
    expect(region?.hasAttribute("hidden")).toBe(false);
    if (!toggle) throw new Error("toggle missing");
    click(toggle);
    expect(onToggle).toHaveBeenCalledWith("medium-1");
    rerender(false);
    expect(container.querySelector("[role='region']")?.hasAttribute("hidden")).toBe(true);

    const trackButton = container.querySelector(".track-row button");
    expect(trackButton?.textContent).toContain("未记录");
    if (!trackButton) throw new Error("track button missing");
    click(trackButton);
    expect(onPlayTrack).toHaveBeenCalledWith(expect.objectContaining({ id: "t1" }));
  });
});

describe("ReleaseDetailDrawer", () => {
  function renderDrawer(state: Parameters<typeof ReleaseDetailDrawer>[0]["state"], overrides: Record<string, unknown> = {}) {
    const handlers = {
      onClose: vi.fn(),
      onRetry: vi.fn(),
      onShowEvidence: vi.fn(),
      onPlayItems: vi.fn(),
      onPlayTrack: vi.fn(),
    };
    render(
      <ReleaseDetailDrawer
        state={state}
        isAdmin={false}
        evidence={null}
        evidenceLoading={false}
        evidenceError=""
        {...handlers}
        {...overrides}
      />,
    );
    return handlers;
  }

  it("moves focus to the close button, closes on Escape, and restores focus", () => {
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();
    const handlers = renderDrawer({ status: "loading" });
    expect(document.activeElement?.getAttribute("aria-label")).toBe("关闭详情");
    const dialog = container.querySelector("[role='dialog']");
    expect(dialog?.getAttribute("aria-modal")).toBe("true");
    const labelIds = dialog?.getAttribute("aria-labelledby")?.split(" ") ?? [];
    expect(labelIds.length).toBeGreaterThan(0);
    expect(labelIds.some((id) => document.getElementById(id) !== null)).toBe(true);

    act(() => {
      document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    expect(handlers.onClose).toHaveBeenCalled();

    act(() => root.unmount());
    mounted = false;
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("traps Tab inside the dialog", () => {
    renderDrawer({ status: "ready", detail: makeDetail() });
    const dialog = container.querySelector("[role='dialog']");
    if (!dialog) throw new Error("dialog missing");
    const focusable = Array.from(dialog.querySelectorAll("button")).filter((element) => !element.hasAttribute("disabled") && element.closest("[hidden]") === null);
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (!first || !last) throw new Error("focusable elements missing");
    act(() => { last.focus(); });
    act(() => {
      last.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
    });
    expect(document.activeElement).toBe(first);
    act(() => {
      first.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
    });
    expect(document.activeElement).toBe(last);
  });

  it("renders classified error states with recovery actions", () => {
    const forbidden = renderDrawer({ status: "error", code: "forbidden" });
    expect(container.textContent).toContain("没有权限");
    expect(forbidden.onRetry).not.toHaveBeenCalled();

    const unavailable = renderDrawer({ status: "error", code: "database_unavailable" });
    const retry = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "重试");
    if (!retry) throw new Error("retry missing");
    click(retry);
    expect(unavailable.onRetry).toHaveBeenCalled();
  });

  it("plays first/all tracks and collapses media locally", () => {
    const handlers = renderDrawer({ status: "ready", detail: makeDetail() });
    const playAll = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "播放全部");
    if (!playAll) throw new Error("play all missing");
    click(playAll);
    expect(handlers.onPlayItems).toHaveBeenCalledWith(expect.arrayContaining([
      expect.objectContaining({ releaseId: "release-1", track: expect.objectContaining({ id: "t1" }) }),
    ]));
    expect(handlers.onPlayItems.mock.calls[0]?.[0]).toHaveLength(2);

    const playFirst = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "播放首曲");
    if (!playFirst) throw new Error("play first missing");
    click(playFirst);
    expect(handlers.onPlayItems.mock.calls[1]?.[0]).toHaveLength(1);

    const collapseAll = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "全部收起");
    if (!collapseAll) throw new Error("collapse all missing");
    click(collapseAll);
    expect(container.querySelector("[role='region']")?.hasAttribute("hidden")).toBe(true);
    const expandAll = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === "全部展开");
    if (!expandAll) throw new Error("expand all missing");
    click(expandAll);
    expect(container.querySelector("[role='region']")?.hasAttribute("hidden")).toBe(false);
  });

  it("hides administrator evidence from ordinary users", () => {
    renderDrawer({ status: "ready", detail: makeDetail() });
    expect(container.textContent).not.toContain("查看完整证据");
  });
});
