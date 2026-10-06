import { describe, expect, it, vi } from "vitest";
import { browserMediaSession } from "./media-session";

describe("浏览器媒体会话适配器", () => {
  it("没有API时返回null", () => {
    vi.stubGlobal("navigator", {});
    expect(browserMediaSession()).toBeNull();
  });

  it("封装元数据、真实状态、位置和媒体键；清理时传null", () => {
    const session = { metadata: null, playbackState: "none", setPositionState: vi.fn(), setActionHandler: vi.fn() };
    vi.stubGlobal("navigator", { mediaSession: session });
    class Metadata {
      constructor(readonly input: unknown) {}
    }
    vi.stubGlobal("MediaMetadata", Metadata);
    const port = browserMediaSession();
    if (port === null) throw new Error("应有媒体会话");
    port.setMetadata({ title: "曲目", artist: "艺人", album: "专辑", artwork: [] });
    expect(session.metadata).toBeInstanceOf(Metadata);
    port.setPlaybackState("playing");
    expect(session.playbackState).toBe("playing");
    port.setPositionState({ duration: 10, position: 2 });
    expect(session.setPositionState).toHaveBeenCalledWith({ duration: 10, position: 2, playbackRate: 1 });
    port.setPositionState(null);
    expect(session.setPositionState).toHaveBeenLastCalledWith();
    const handler = vi.fn();
    port.setActionHandler("seekto", handler);
    const callback = session.setActionHandler.mock.calls[0]?.[1] as (details: { seekTime: number }) => void;
    callback({ seekTime: 4 });
    expect(handler).toHaveBeenCalledWith({ seekTime: 4 });
    port.setActionHandler("seekto", null);
    expect(session.setActionHandler).toHaveBeenLastCalledWith("seekto", null);
    port.setMetadata(null);
    expect(session.metadata).toBeNull();
  });

  it("部分API缺失或浏览器拒绝状态/动作不影响页面播放", () => {
    vi.stubGlobal("navigator", { mediaSession: {
      set metadata(_value: unknown) { throw new Error("不可写"); },
      set playbackState(_value: unknown) { throw new Error("不可写"); },
      setPositionState: () => { throw new Error("不支持"); },
      setActionHandler: () => { throw new Error("不支持"); },
    } });
    vi.stubGlobal("MediaMetadata", undefined);
    const port = browserMediaSession();
    expect(() => {
      port?.setMetadata({ title: "曲目", artist: "艺人", album: "专辑", artwork: [] });
      port?.setPlaybackState("paused");
      port?.setPositionState(null);
      port?.setActionHandler("seekbackward", () => undefined);
    }).not.toThrow();
  });
});
