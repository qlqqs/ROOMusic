import { artistWebSearchOf } from "../../../test/artist-web-search-data";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { createElement, type PropsWithChildren } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import type { ArtistWebSearchDTO } from "../api/decoders";
import { fetchArtistWebSearch } from "../api/endpoints";
import { bioLangOf, useArtistMedia } from "./use-artist-media";

vi.mock("../../auth", async (importOriginal) => {
  const scope = { generation: 1, isCurrent: () => true };
  return { ...await importOriginal<typeof import("../../auth")>(), useViewerScope: () => scope };
});

vi.mock("../api/endpoints", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/endpoints")>(),
  fetchArtistWebSearch: vi.fn(),
}));

const artistId = "00000000-0000-4000-8000-000000000001";
const revision = 12;
const path = `/api/v1/artists/${artistId}`;
const idempotencyKey = expect.stringMatching(/^[0-9a-f-]{36}$/);

function setup() {
  const refresh = vi.fn<() => Promise<void>>().mockResolvedValue(undefined);
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (url) =>
    Response.json(sampleBody(url === `${path}/bio` ? "artists/set-bio" : "artists/reject-media")),
  );
  vi.stubGlobal("fetch", fetch);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  const wrapper = ({ children }: PropsWithChildren) => createElement(QueryClientProvider, { client: queryClient }, children);
  const hook = renderHook(
    (props: { revision: number }) => useArtistMedia({ artistId, revision: props.revision, refresh }),
    { initialProps: { revision }, wrapper },
  );
  return { ...hook, fetch, refresh };
}

function expectReject(fetch: ReturnType<typeof vi.fn<typeof globalThis.fetch>>, call: number, field: "image" | "bio", keepalive = false) {
  expect(fetch).toHaveBeenNthCalledWith(call, `${path}/media/reject`, expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ expected_revision: revision, field }),
  }));
  const init = fetch.mock.calls[call - 1]?.[1];
  expect(new Headers(init?.headers).get("Idempotency-Key")).toEqual(idempotencyKey);
  expect(init?.keepalive).toBe(keepalive ? true : undefined);
}

function advance(ms: number) {
  return act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

describe("useArtistMedia", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(fetchArtistWebSearch).mockReset().mockResolvedValue({ status: 200, data: artistWebSearchOf({ state: "none" }) });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("立即隐藏被标记的图片，满 6 秒才提交并刷新", async () => {
    const { result, fetch, refresh } = setup();
    act(() => result.current.reject("image"));
    expect(result.current.hidden).toEqual({ image: true, bio: false });
    expect(result.current.pendingReject?.field).toBe("image");
    await advance(5999);
    expect(fetch).not.toHaveBeenCalled();
    await advance(1);
    expectReject(fetch, 1, "image");
    expect(result.current.pendingReject).toBeNull();
    expect(refresh).toHaveBeenCalledOnce();
  });

  it("撤回立即恢复原资料，过期后也不发请求", async () => {
    const { result, fetch, refresh } = setup();
    act(() => result.current.reject("bio"));
    expect(result.current.hidden.bio).toBe(true);
    await advance(5999);
    act(() => result.current.undoReject());
    expect(result.current.hidden.bio).toBe(false);
    expect(result.current.pendingReject).toBeNull();
    await advance(6000);
    expect(fetch).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it("再标记另一项时立即提交上一项，第二项仍能撤回", async () => {
    const { result, fetch } = setup();
    act(() => result.current.reject("image"));
    await advance(3000);
    await act(async () => result.current.reject("bio"));
    expect(fetch).toHaveBeenCalledOnce();
    expectReject(fetch, 1, "image");
    expect(result.current.pendingReject?.field).toBe("bio");
    expect(result.current.hidden.bio).toBe(true);
    act(() => result.current.undoReject());
    await advance(6000);
    expect(fetch).toHaveBeenCalledOnce();
  });

  it("连续标记图片和简介时，第二项提交使用上一项写入后的版本号", async () => {
    const { result, fetch, rerender } = setup();
    act(() => result.current.reject("image"));
    await act(async () => result.current.reject("bio"));
    expectReject(fetch, 1, "image");

    rerender({ revision: revision + 1 });
    await advance(6000);
    expect(fetch).toHaveBeenNthCalledWith(2, `${path}/media/reject`, expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ expected_revision: revision + 1, field: "bio" }),
    }));
  });

  it.each(["pagehide", "卸载"])("%s 时用 keepalive 提交且不重复提交", async (trigger) => {
    const { result, fetch, unmount } = setup();
    act(() => result.current.reject("bio"));
    if (trigger === "pagehide") {
      await act(async () => { window.dispatchEvent(new Event("pagehide")); });
    }
    unmount();
    await advance(6000);
    expect(fetch).toHaveBeenCalledOnce();
    expectReject(fetch, 1, "bio", true);
  });

  it("后端报版本冲突时说明原因，并在刷新完成后恢复显示", async () => {
    const { result, fetch, refresh } = setup();
    fetch.mockResolvedValueOnce(Response.json(sampleBody("artists/errors/revision-conflict"), { status: 409 }));
    let finishRefresh = () => {};
    refresh.mockImplementationOnce(() => new Promise<void>((resolve) => { finishRefresh = resolve; }));

    act(() => result.current.reject("image"));
    await advance(6000);
    expect(result.current.error).toBe("艺人刚被修改过，页面已刷新，请再试一次。");
    expect(result.current.hidden.image).toBe(true);
    expect(refresh).toHaveBeenCalledOnce();

    await act(async () => finishRefresh());
    expect(result.current.hidden.image).toBe(false);
    expect(result.current.pendingReject).toBeNull();
  });

  it.each([
    [new File(["合成文本"], "test.txt", { type: "text/plain" }), "请选择 JPEG、PNG 或 WebP 图片。"],
    [new File([new Uint8Array(16 * 1024 * 1024 + 1)], "large.png", { type: "image/png" }), "图片太大了：请选择 16 MB 以内的图片。"],
  ])("上传前拦截不合规图片并给出原因 %#", (file, message) => {
    const { result } = setup();
    const open = vi.spyOn(XMLHttpRequest.prototype, "open");
    try {
      act(() => result.current.uploadImage(file));
      expect(result.current.error).toBe(message);
      expect(result.current.upload.status).toBe("idle");
      expect(open).not.toHaveBeenCalled();
    } finally {
      open.mockRestore();
    }
  });

  it.each([["English biography 🎵", "en"], ["English 与中文", "zh"]])(
    "保存简介 %s 时提交自动识别的语言",
    async (text, lang) => {
      const { result, fetch, refresh } = setup();
      await act(async () => { expect(await result.current.saveBio(text)).toBe(true); });
      expect(fetch).toHaveBeenCalledWith(`${path}/bio`, expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ expected_revision: revision, text, lang }),
      }));
      expect(refresh).toHaveBeenCalledOnce();
      expect(result.current.savingBio).toBe(false);
    },
  );

  it.each(["queued", "running"] as const)("网络搜索 %s 每 20 秒重读，候选待确认后停止轮询", async (state) => {
    const status = vi.mocked(fetchArtistWebSearch);
    status.mockResolvedValueOnce({ status: 200, data: artistWebSearchOf({ state }) });
    const { result } = setup();

    await advance(1);
    expect(result.current.webSearch?.state).toBe(state);
    expect(status).toHaveBeenCalledExactlyOnceWith(artistId, expect.any(AbortSignal));

    status.mockResolvedValue({ status: 200, data: artistWebSearchOf({ state: "waiting_human" }) });
    await advance(19_998);
    expect(status).toHaveBeenCalledOnce();
    await advance(10);
    expect(result.current.webSearch?.state).toBe("waiting_human");
    expect(status).toHaveBeenCalledTimes(2);
    await advance(60_000);
    expect(status).toHaveBeenCalledTimes(2);
  });

  it.each<ArtistWebSearchDTO["state"]>(["none", "shelved", "waiting_human"])("网络搜索 %s 只读取一次", async (state) => {
    const status = vi.mocked(fetchArtistWebSearch);
    status.mockResolvedValue({ status: 200, data: artistWebSearchOf({ state }) });
    const { result } = setup();
    await advance(60_000);
    expect(result.current.webSearch?.state).toBe(state);
    expect(status).toHaveBeenCalledOnce();
  });

  it("全部不采纳时逐个提交候选，完成后刷新资料", async () => {
    const { result, fetch, refresh } = setup();
    const ids = ["00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"];
    let finishFirst = () => {};
    fetch.mockImplementationOnce(() => new Promise<Response>((resolve) => {
      finishFirst = () => resolve(Response.json(sampleBody("curation/artist-web-search/candidate-decline")));
    }));
    fetch.mockResolvedValueOnce(Response.json(sampleBody("curation/artist-web-search/candidate-decline")));
    let decision: Promise<boolean> | undefined;

    act(() => { decision = result.current.decideCandidates(ids, "decline"); });
    expect(result.current.deciding).toBe(true);
    expect(fetch).toHaveBeenCalledOnce();
    expect(refresh).not.toHaveBeenCalled();

    await act(async () => {
      finishFirst();
      expect(await decision).toBe(true);
    });
    expect(fetch).toHaveBeenCalledTimes(2);
    for (const [index, id] of ids.entries()) {
      expect(fetch).toHaveBeenNthCalledWith(index + 1, `/api/v1/admin/curation/exceptions/${id}/decline`, expect.objectContaining({ method: "POST" }));
    }
    expect(refresh).toHaveBeenCalledOnce();
    expect(result.current.deciding).toBe(false);
  });
});

it.each([["Biography", "en"], ["かな 🎵", "en"], ["English 和中文", "zh"], ["扩展区 𠮷", "zh"], ["𠮷", "zh"]])(
  "bioLangOf 根据汉字识别 %s",
  (text, lang) => expect(bioLangOf(text)).toBe(lang),
);
