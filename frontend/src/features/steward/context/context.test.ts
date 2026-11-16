import { describe, expect, it } from "vitest";
import { noDismissed, pageIdentityOf, playbackIdentityOf, requestContextOf, visibleContextOf, type StewardPageContext, type StewardPlaybackContext } from "./context";

const page: StewardPageContext = { path: "/albums/00000000-0000-4000-8000-000000000001", kind: "album", id: "00000000-0000-4000-8000-000000000001", title: "Bayreuth 1951" };
const playback: StewardPlaybackContext = { zone_id: "00000000-0000-4000-8000-00000000e001", zone_name: "客厅", track_id: "00000000-0000-4000-8000-0000000000f1", title: "Presto", artist: "Furtwängler", status: "playing" };

describe("请求体里的页面上下文", () => {
  it("两项都在时都带上；都没有时是 null", () => {
    expect(requestContextOf(page, playback, noDismissed)).toEqual({ page, playback });
    expect(requestContextOf(null, null, noDismissed)).toBeNull();
  });

  it("点掉的项不带；两项都点掉时是 null", () => {
    expect(requestContextOf(page, playback, { page: null, playback: playbackIdentityOf(playback) })).toEqual({ page });
    expect(requestContextOf(page, playback, { page: pageIdentityOf(page), playback: playbackIdentityOf(playback) })).toBeNull();
  });

  it("点掉的是别的页面或别的曲目时，新的照常显示与发送", () => {
    const dismissed = { page: "/me/history", playback: playbackIdentityOf({ ...playback, track_id: "00000000-0000-4000-8000-0000000000f2" }) };
    expect(visibleContextOf(page, playback, dismissed)).toEqual({ page, playback });
    // 只是标题读到了或状态变了，仍是点掉的那一项。
    const untitled: StewardPageContext = { path: page.path, kind: page.kind };
    expect(visibleContextOf(untitled, { ...playback, status: "paused" }, { page: pageIdentityOf(page), playback: playbackIdentityOf(playback) })).toEqual({ page: null, playback: null });
  });

  it("超过后端上限的路径与标题按字符截断", () => {
    const long = "交".repeat(400);
    const context = requestContextOf({ ...page, path: `/${"a".repeat(600)}`, title: long }, { ...playback, zone_name: long, title: long, artist: long }, noDismissed);
    expect(Array.from(context?.page?.title ?? "")).toHaveLength(300);
    expect(context?.page?.path).toHaveLength(512);
    expect(Array.from(context?.playback?.zone_name ?? "")).toHaveLength(300);
    expect(Array.from(context?.playback?.title ?? "")).toHaveLength(300);
    expect(Array.from(context?.playback?.artist ?? "")).toHaveLength(300);
  });
});
