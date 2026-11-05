import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { decode as d } from "../../shared/api";
import { deferred } from "../../test/deferred";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { jsonResponse, sampleResponse, type RecordedRequest } from "../../test/http";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../auth";
import { MAX_LOOKUP_ALBUMS, MAX_LOOKUP_TRACKS, type FavoriteLookupDTO, type FavoriteToggleDTO } from "./api/decoders";
import { FavoriteButton, type FavoriteTarget } from "./components/favorite-button";
import { FavoriteStatusScope } from "./components/favorite-status-scope";

const trackA: FavoriteTarget = { kind: "track", id: "00000000-0000-4000-8000-0000000000a1", title: "第一首" };
const trackB: FavoriteTarget = { kind: "track", id: "00000000-0000-4000-8000-0000000000b2", title: "第二首" };
const album: FavoriteTarget = { kind: "album", id: "00000000-0000-4000-8000-0000000000c3", title: "测试专辑" };
const lookupPath = "/api/v1/me/favorites/lookup";
const targetKey = (target: FavoriteTarget) => `${target.kind}:${target.id.toLowerCase()}`;
const targetPath = (target: FavoriteTarget) => `/api/v1/me/favorites/${target.kind === "track" ? "tracks" : "albums"}/${target.id}`;
const readLookupIds = d.defineDecoder(d.object({ track_ids: d.array(d.uuid(), { max: MAX_LOOKUP_TRACKS }), group_ids: d.array(d.uuid(), { max: MAX_LOOKUP_ALBUMS }) }));
const dispose: (() => void)[] = [];

afterEach(() => {
  cleanup();
  for (const release of dispose.splice(0)) release();
});

/** 只替换 HTTP，组件、会话、查询缓存与收藏 hook 均使用真实实现。 */
async function setup(initial: readonly FavoriteTarget[] = []) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const server = new Set(initial.map(targetKey));
  let revision = 0;
  const lookup = (request: RecordedRequest) => {
    const ids = readLookupIds(request.body);
    const body: FavoriteLookupDTO = {
      revision,
      tracks: ids.track_ids.map((id) => ({ track_id: id, favorited: server.has(`track:${id.toLowerCase()}`) })),
      albums: ids.group_ids.map((id) => ({ group_id: id, favorited: server.has(`album:${id.toLowerCase()}`) })),
      artists: [],
    };
    return jsonResponse(200, body);
  };
  const commit = (target: FavoriteTarget, favorited: boolean) => {
    if (server.has(targetKey(target)) !== favorited) revision++;
    if (favorited) server.add(targetKey(target));
    else server.delete(targetKey(target));
    const body: FavoriteToggleDTO = {
      target_kind: target.kind, target_id: target.id, favorited,
      created_at: favorited ? "2026-11-04T00:00:00Z" : null, revision,
    };
    return jsonResponse(200, body);
  };
  api.on("POST", lookupPath, lookup);
  for (const target of [trackA, trackB, album]) {
    api.on("PUT", targetPath(target), () => commit(target, true));
    api.on("DELETE", targetPath(target), () => commit(target, false));
  }
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("测试会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000, refetchOnWindowFocus: false } } });
  dispose.push(() => { client.clear(); controller.dispose(); });
  const view = (children: ReactNode) => render(
    <QueryClientProvider client={client}>
      <SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>{children}</ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>,
  );
  return { api, client, commit, lookup, view, user: userEvent.setup() };
}

function FavoriteArea({ name = "列表", targets }: { name?: string; targets: readonly FavoriteTarget[] }) {
  return <section aria-label={name}>
    <FavoriteStatusScope trackIds={targets.filter((target) => target.kind === "track").map((target) => target.id)} groupIds={targets.filter((target) => target.kind === "album").map((target) => target.id)}>
      {targets.map((target) => <FavoriteButton key={targetKey(target)} target={target} />)}
    </FavoriteStatusScope>
  </section>;
}

function heart(target: FavoriteTarget, area = "列表") {
  return within(screen.getByRole("region", { name: area })).getByRole("button", { name: `收藏《${target.title}》` });
}

async function ready(target: FavoriteTarget, favorited = false, area = "列表") {
  const button = heart(target, area);
  await waitFor(() => {
    expect(button.hasAttribute("disabled")).toBe(false);
    expect(button.getAttribute("aria-pressed")).toBe(String(favorited));
    expect(button.getAttribute("aria-busy")).toBeNull();
  });
  return button;
}

const directions = [
  { name: "曲目收藏", target: trackA, initially: false },
  { name: "曲目取消", target: trackA, initially: true },
  { name: "专辑收藏", target: album, initially: false },
  { name: "专辑取消", target: album, initially: true },
];

describe("心形的真实交互与请求时序", () => {
  it("一批曲目与专辑只查一次，读到之前不能切换", async () => {
    const { api, lookup, user, view } = await setup([trackA]);
    const response = deferred<void>();
    api.on("POST", lookupPath, async (request) => { await response.promise; return lookup(request); });
    view(<FavoriteArea targets={[trackA, trackB, album]} />);
    for (const target of [trackA, trackB, album]) {
      expect(heart(target).hasAttribute("disabled")).toBe(true);
      expect(heart(target).getAttribute("title")).toBe("正在读取收藏状态");
      await user.click(heart(target));
    }
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(1);
    expect(api.requestsTo("POST", lookupPath)[0]?.body).toEqual({ track_ids: [trackA.id, trackB.id], group_ids: [album.id] });
    expect(api.requests.filter((request) => request.method === "PUT" || request.method === "DELETE")).toHaveLength(0);
    await act(async () => response.resolve());
    await ready(trackA, true);
    await ready(trackB);
    await ready(album);
  });

  it.each(directions)("$name 在回执到达前乐观切换，成功后保持且不丢焦点", async ({ target, initially }) => {
    const { api, commit, client, user, view } = await setup(initially ? [target] : []);
    const response = deferred<Response>();
    const method = initially ? "DELETE" : "PUT";
    api.on(method, targetPath(target), () => response.promise);
    view(<FavoriteArea targets={[target]} />);
    const button = await ready(target, initially);
    await user.click(button);
    await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe(String(!initially)));
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.getAttribute("aria-disabled")).toBe("true");
    expect(button.hasAttribute("disabled")).toBe(false);
    expect(document.activeElement).toBe(button);
    expect(api.requestsTo(method, targetPath(target))).toHaveLength(1);
    expect(api.requestsTo(method, targetPath(target))[0]?.body).toBeUndefined();
    await act(async () => response.resolve(commit(target, !initially)));
    await ready(target, !initially);
    await waitFor(() => expect(client.isFetching()).toBe(0));
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(document.activeElement).toBe(button);
  });

  it.each(directions)("$name 被明确拒绝时回到原状态，并在心形旁提示失败", async ({ target, initially }) => {
    const { api, user, view } = await setup(initially ? [target] : []);
    const response = deferred<Response>();
    const method = initially ? "DELETE" : "PUT";
    api.on(method, targetPath(target), () => response.promise);
    view(<FavoriteArea targets={[target]} />);
    const button = await ready(target, initially);
    await user.click(button);
    await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe(String(!initially)));
    await act(async () => response.resolve(sampleResponse("errors/personal-origin-forbidden")));
    await ready(target, initially);
    expect(screen.getByRole("status").textContent).toBe(initially ? "没能取消收藏，请稍后再试。" : "没能收藏，请稍后再试。");
    expect(api.requestsTo(method, targetPath(target))).toHaveLength(1);
  });

  it.each(["先失败", "先成功"])("两目标同时切换，%s 都不会让一个失败回滚另一个", async (order) => {
    const { api, commit, client, user, view } = await setup();
    const first = deferred<Response>();
    const second = deferred<Response>();
    api.on("PUT", targetPath(trackA), () => first.promise);
    api.on("PUT", targetPath(trackB), () => second.promise);
    view(<FavoriteArea targets={[trackA, trackB]} />);
    await ready(trackA);
    await ready(trackB);
    await user.click(heart(trackA));
    await user.click(heart(trackB));
    await waitFor(() => {
      expect(heart(trackA).getAttribute("aria-pressed")).toBe("true");
      expect(heart(trackB).getAttribute("aria-pressed")).toBe("true");
    });
    if (order === "先失败") {
      await act(async () => first.resolve(sampleResponse("errors/personal-origin-forbidden")));
      await ready(trackA);
      expect(heart(trackB).getAttribute("aria-pressed")).toBe("true");
      expect(heart(trackB).getAttribute("aria-busy")).toBe("true");
      await act(async () => second.resolve(commit(trackB, true)));
    } else {
      await act(async () => second.resolve(commit(trackB, true)));
      await ready(trackB, true);
      expect(heart(trackA).getAttribute("aria-busy")).toBe("true");
      await act(async () => first.resolve(sampleResponse("errors/personal-origin-forbidden")));
    }
    await ready(trackA);
    await ready(trackB, true);
    await waitFor(() => expect(client.isFetching()).toBe(0));
    expect(heart(trackB).getAttribute("aria-pressed")).toBe("true");
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("PUT", targetPath(trackB))).toHaveLength(1);
    expect(screen.getByText("没能收藏，请稍后再试。")).toBeTruthy();
  });

  it("同目标跨位置共享进行中状态，连点与键盘再次激活都不再发请求", async () => {
    const { api, commit, user, view } = await setup();
    const response = deferred<Response>();
    api.on("PUT", targetPath(trackA), () => response.promise);
    view(<><FavoriteArea targets={[trackA, trackB]} /><FavoriteArea name="播放器" targets={[trackA]} /></>);
    const first = await ready(trackA);
    const second = await ready(trackA, false, "播放器");
    await user.click(first);
    await waitFor(() => expect(second.getAttribute("aria-busy")).toBe("true"));
    await user.dblClick(first);
    await user.click(second);
    await user.keyboard("{Enter} ");
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(0);
    expect(second.getAttribute("aria-pressed")).toBe("true");
    await act(async () => response.resolve(commit(trackA, true)));
    await ready(trackA, true);
    await ready(trackA, true, "播放器");
  });

  it("一处切换立即更新不同批次里的同目标，回执确认后等待重读也不闪回", async () => {
    const { api, commit, lookup, user, view } = await setup();
    view(<><FavoriteArea targets={[trackA, trackB, album]} /><FavoriteArea name="播放器" targets={[trackA]} /></>);
    const first = await ready(trackA);
    await ready(trackA, false, "播放器");
    const response = deferred<Response>();
    const reread = deferred<void>();
    api.on("PUT", targetPath(trackA), () => response.promise);
    api.on("POST", lookupPath, async (request) => { await reread.promise; return lookup(request); });
    await user.click(first);
    await waitFor(() => expect(heart(trackA, "播放器").getAttribute("aria-pressed")).toBe("true"));
    await act(async () => response.resolve(commit(trackA, true)));
    await waitFor(() => expect(api.requestsTo("POST", lookupPath)).toHaveLength(4));
    await ready(trackA, true);
    await ready(trackA, true, "播放器");
    expect(heart(trackB).getAttribute("aria-pressed")).toBe("false");
    expect(heart(album).getAttribute("aria-pressed")).toBe("false");
    await act(async () => reread.resolve());
    await user.click(heart(trackA, "播放器"));
    await ready(trackA);
    await ready(trackA, false, "播放器");
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(1);
  });

  it.each([
    { name: "网络中断", fail: (): Response => { throw new TypeError("模拟响应途中断开"); } },
    { name: "服务端 503", fail: () => sampleResponse("errors/personal-database-unavailable") },
    { name: "成功响应损坏", fail: () => jsonResponse(200, { favorited: true }) },
  ])("$name 后先回滚显示并等待重读，确认服务端已收藏后才允许取消", async ({ fail }) => {
    const { api, commit, lookup, user, view } = await setup();
    const response = deferred<void>();
    const reread = deferred<void>();
    api.on("PUT", targetPath(trackA), async () => {
      await response.promise;
      commit(trackA, true);
      return fail();
    });
    view(<FavoriteArea targets={[trackA]} />);
    const button = await ready(trackA);
    api.on("POST", lookupPath, async (request) => { await reread.promise; return lookup(request); });
    await user.click(button);
    await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe("true"));
    await act(async () => response.resolve());
    expect(await screen.findByText("不确定是否成功，正在重新读取。")).toBeTruthy();
    await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe("false"));
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    await user.dblClick(button);
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(0);
    await act(async () => reread.resolve());
    await ready(trackA, true);
    await user.click(button);
    await ready(trackA);
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(1);
  });

  it("一处结果不确定时，其他位置的同目标也必须等重读完成才能切换", async () => {
    const { api, commit, lookup, user, view } = await setup();
    view(<><FavoriteArea targets={[trackA, trackB]} /><FavoriteArea name="播放器" targets={[trackA]} /></>);
    const first = await ready(trackA);
    const second = await ready(trackA, false, "播放器");
    const reread = deferred<void>();
    api.on("POST", lookupPath, async (request) => { await reread.promise; return lookup(request); });
    api.on("PUT", targetPath(trackA), () => {
      commit(trackA, true);
      return sampleResponse("errors/personal-database-unavailable");
    });
    await user.click(first);
    expect(await screen.findByText("不确定是否成功，正在重新读取。")).toBeTruthy();
    await waitFor(() => expect(api.requestsTo("POST", lookupPath)).toHaveLength(4));
    const busyWhileReading = [first.getAttribute("aria-busy"), second.getAttribute("aria-busy")];
    await user.click(second);
    const writesWhileReading = api.requestsTo("PUT", targetPath(trackA)).length;
    await act(async () => reread.resolve());
    await ready(trackA, true);
    await ready(trackA, true, "播放器");
    expect({ busyWhileReading, writesWhileReading }).toEqual({ busyWhileReading: ["true", "true"], writesWhileReading: 1 });
  });

  it("不确定结果的重读也失败时，不能用旧显示解锁并再次切换", async () => {
    const { api, commit, client, user, view } = await setup();
    view(<FavoriteArea targets={[trackA]} />);
    const button = await ready(trackA);
    api.on("POST", lookupPath, () => sampleResponse("errors/personal-database-unavailable"));
    api.on("PUT", targetPath(trackA), () => {
      commit(trackA, true);
      return sampleResponse("errors/personal-database-unavailable");
    });
    await user.click(button);
    expect(await screen.findByText("没能确认是否已收藏。")).toBeTruthy();
    await waitFor(() => {
      expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
      expect(client.isFetching()).toBe(0);
    });
    expect(button.getAttribute("aria-pressed")).toBe("false");
    expect(button.getAttribute("aria-busy")).toBe("true");
    await user.click(button);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(client.isFetching()).toBe(0);
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(0);
  });

  it.each([true, false])("重读失败后从另一位置重新读取，两位置按服务端值 %s 解锁并能正常切换", async (serverFavorited) => {
    const { api, commit, lookup, client, user, view } = await setup();
    view(<><FavoriteArea targets={[trackA, trackB]} /><FavoriteArea name="播放器" targets={[trackA]} /></>);
    const first = await ready(trackA);
    const second = await ready(trackA, false, "播放器");
    api.on("POST", lookupPath, () => sampleResponse("errors/personal-database-unavailable"));
    api.on("PUT", targetPath(trackA), () => {
      if (serverFavorited) commit(trackA, true);
      return sampleResponse("errors/personal-database-unavailable");
    });
    await user.click(first);
    for (const area of ["列表", "播放器"]) {
      expect(await within(screen.getByRole("region", { name: area })).findByText("没能确认是否已收藏。")).toBeTruthy();
    }
    await waitFor(() => expect(client.isFetching()).toBe(0));
    for (const button of [first, second]) {
      expect(button.getAttribute("aria-pressed")).toBe("false");
      expect(button.getAttribute("aria-busy")).toBe("true");
      await user.click(button);
    }
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(4);
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(0);

    const reread = deferred<void>();
    api.on("POST", lookupPath, async (request) => { await reread.promise; return lookup(request); });
    await user.click(within(screen.getByRole("region", { name: "播放器" })).getByRole("button", { name: `重新读取《${trackA.title}》的收藏状态` }));
    await waitFor(() => expect(api.requestsTo("POST", lookupPath)).toHaveLength(6));
    for (const button of [first, second]) {
      expect(button.getAttribute("aria-busy")).toBe("true");
      expect(button.getAttribute("aria-pressed")).toBe("false");
    }
    await act(async () => reread.resolve());
    await ready(trackA, serverFavorited);
    await ready(trackA, serverFavorited, "播放器");
    await waitFor(() => expect(client.isFetching()).toBe(0));
    expect(screen.queryByText("没能确认是否已收藏。")).toBeNull();
    expect(screen.queryByRole("button", { name: `重新读取《${trackA.title}》的收藏状态` })).toBeNull();
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(1);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(0);

    api.on("PUT", targetPath(trackA), () => commit(trackA, true));
    await user.click(second);
    await ready(trackA, !serverFavorited);
    await ready(trackA, !serverFavorited, "播放器");
    expect(api.requestsTo("PUT", targetPath(trackA))).toHaveLength(serverFavorited ? 1 : 2);
    expect(api.requestsTo("DELETE", targetPath(trackA))).toHaveLength(serverFavorited ? 1 : 0);
  });
});
