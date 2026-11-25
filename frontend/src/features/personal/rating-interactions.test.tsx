import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { MenuButton } from "../../shared/ui";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { sampleResponse } from "../../test/http";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../auth";
import { setRating } from "./api/ratings";
import { RatingControl, type RatingTarget } from "./components/rating-control";
import { useRatingMenuAction } from "./components/rating-menu-action";
import { RatingBadge } from "./components/rating-badge";
import { RatingStatusScope, useRatingStatus } from "./components/rating-status-scope";

const track: RatingTarget = { kind: "track", id: "00000000-0000-4000-8000-0000000000a1", title: "第一首" };
const album: RatingTarget = { kind: "album", id: "00000000-0000-4000-8000-0000000000c3", title: "测试专辑" };
const artist: RatingTarget = { kind: "artist", id: "00000000-0000-4000-8000-0000000000d4", title: "测试艺人" };
const trackPath = `/api/v1/me/ratings/tracks/${track.id}`;
const albumPath = `/api/v1/me/ratings/albums/${album.id}`;
const dispose: (() => void)[] = [];

afterEach(() => {
  cleanup();
  for (const release of dispose.splice(0)) release();
});

/** 只替换 HTTP（评分接口用 fake-personal 的内存替身），组件、会话、查询缓存与写入 hook 均是真实实现。 */
async function setup(seed: { target: RatingTarget; rating: number }[] = []) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("测试会话未就绪");
  for (const { target, rating } of seed) await setRating(target.kind, target.id, rating);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000, refetchOnWindowFocus: false } } });
  dispose.push(() => {
    client.clear();
    controller.dispose();
  });
  const view = (children: ReactNode) =>
    render(
      <QueryClientProvider client={client}>
        <SessionProvider controller={controller}>
          <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
            {children}
          </ViewerScopeProvider>
        </SessionProvider>
      </QueryClientProvider>,
    );
  return { api, view, user: userEvent.setup() };
}

function Header({ name = "头部", target = album }: { name?: string; target?: RatingTarget }) {
  return (
    <section aria-label={name}>
      <RatingStatusScope
        trackIds={target.kind === "track" ? [target.id] : []}
        groupIds={target.kind === "album" ? [target.id] : []}
        artistIds={target.kind === "artist" ? [target.id] : []}
      >
        <RatingControl target={target} />
      </RatingStatusScope>
    </section>
  );
}

function TrackRow() {
  const { action, popover } = useRatingMenuAction(track);
  const ready = useRatingStatus().ratingOf(track.kind, track.id) !== undefined;
  return (
    <div>
      {ready ? <span data-testid="ratings-ready" /> : null}
      <RatingBadge kind="track" id={track.id} />
      <MenuButton label="更多操作" iconOnly icon={<span>…</span>} actions={[action]} />
      {popover}
    </div>
  );
}

const slider = (name: string) => screen.findByRole("slider", { name });

describe("星级评分", () => {
  it("读屏名是“评分《标题》”，值读作“N 星”；艺人用引号", async () => {
    const { view } = await setup([{ target: album, rating: 4 }]);
    view(
      <>
        <Header />
        <Header name="艺人" target={artist} />
      </>,
    );
    await waitFor(async () => expect((await slider("评分《测试专辑》")).getAttribute("aria-valuetext")).toBe("4 星"));
    expect((await slider("评分“测试艺人”")).getAttribute("aria-valuetext")).toBe("未评分");
    expect((await slider("评分《测试专辑》")).getAttribute("aria-valuenow")).toBe("4");
  });

  it("方向键只预览不写入，Enter 才写入一次；Esc 放弃预览", async () => {
    const { api, view, user } = await setup();
    view(<Header />);
    const stars = await slider("评分《测试专辑》");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("未评分"));
    stars.focus();
    await user.keyboard("{ArrowRight}{ArrowRight}{ArrowRight}");
    expect(stars.getAttribute("aria-valuetext")).toBe("3 星，按 Enter 保存");
    await user.keyboard("{Escape}");
    expect(stars.getAttribute("aria-valuetext")).toBe("未评分");
    await user.keyboard("{ArrowRight}{ArrowRight}{Enter}");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("2 星"));
    const writes = api.requestsTo("PUT", albumPath);
    expect(writes).toHaveLength(1);
    expect(writes[0]?.body).toEqual({ rating: 2 });
  });

  it("数字键直接写入，Delete 清除；点当前星级也清除", async () => {
    const { api, view, user } = await setup([{ target: album, rating: 2 }]);
    view(<Header />);
    const stars = await slider("评分《测试专辑》");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("2 星"));
    stars.focus();
    await user.keyboard("5");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("5 星"));
    await user.keyboard("{Delete}");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("未评分"));
    expect(api.requestsTo("DELETE", albumPath)).toHaveLength(1);
    // 鼠标：点第 3 颗写入 3 星，再点第 3 颗清除。
    const third = stars.children[2] as HTMLElement;
    await user.click(third);
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("3 星"));
    await user.click(third);
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("未评分"));
    expect(api.requestsTo("DELETE", albumPath)).toHaveLength(2);
  });

  it("写入失败时回到原值，并在旁边说一句", async () => {
    const { api, view, user } = await setup([{ target: album, rating: 4 }]);
    api.on("PUT", albumPath, () => sampleResponse("errors/personal-invalid-input"));
    view(<Header />);
    const stars = await slider("评分《测试专辑》");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("4 星"));
    stars.focus();
    await user.keyboard("1");
    expect(await screen.findByText("评分没有保存，请稍后再试。")).toBeTruthy();
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("4 星"));
  });

  it("读不到评分时不可用，读屏说明原因", async () => {
    const { api, view, user } = await setup();
    api.on("POST", "/api/v1/me/ratings/lookup", () => sampleResponse("errors/personal-database-unavailable"));
    view(<Header />);
    const stars = await slider("评分《测试专辑》");
    await waitFor(() => expect(stars.getAttribute("aria-valuetext")).toBe("暂时读不到评分"));
    expect(stars.getAttribute("aria-disabled")).toBe("true");
    stars.focus();
    await user.keyboard("3");
    expect(api.requestsTo("PUT", albumPath)).toHaveLength(0);
  });

  it("一处写入，同一目标的其他位置随之更新", async () => {
    const { view, user } = await setup();
    view(
      <>
        <Header name="甲" />
        <Header name="乙" />
      </>,
    );
    const first = await within(await screen.findByRole("region", { name: "甲" })).findByRole("slider");
    const second = within(screen.getByRole("region", { name: "乙" })).getByRole("slider");
    await waitFor(() => expect(first.getAttribute("aria-valuetext")).toBe("未评分"));
    first.focus();
    await user.keyboard("4");
    await waitFor(() => expect(second.getAttribute("aria-valuetext")).toBe("4 星"));
  });

  it("“更多操作”的“评分…”弹出浮层，焦点在星级上；写入后关闭并把焦点还给“更多操作”，行里显示只读小星", async () => {
    const { api, view, user } = await setup();
    view(
      <RatingStatusScope trackIds={[track.id]}>
        <TrackRow />
      </RatingStatusScope>,
    );
    const more = screen.getByRole("button", { name: "更多操作" });
    await screen.findByTestId("ratings-ready");
    expect(screen.queryByRole("img", { name: /星$/ })).toBeNull();
    await user.click(more);
    await user.click(await screen.findByRole("menuitem", { name: "评分…" }));
    const dialog = await screen.findByRole("dialog", { name: "评分《第一首》" });
    const stars = within(dialog).getByRole("slider", { name: "评分《第一首》" });
    expect(document.activeElement).toBe(stars);
    await user.keyboard("3");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), { timeout: 2_000 });
    expect(document.activeElement).toBe(more);
    expect(api.requestsTo("PUT", trackPath)[0]?.body).toEqual({ rating: 3 });
    expect(screen.getByRole("img", { name: "3 星" })).toBeTruthy();
    // 再打开：提示列是当前星级，浮层里有“清除评分”。
    await user.click(more);
    expect(await screen.findByRole("menuitem", { name: "评分… 3 星" })).toBeTruthy();
    await user.click(screen.getByRole("menuitem", { name: "评分… 3 星" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "清除评分" }));
    await waitFor(() => expect(api.requestsTo("DELETE", trackPath)).toHaveLength(1));
  });

  it("浮层里按 Esc：有预览时只放弃预览，再按一次关闭并还焦点", async () => {
    const { view, user } = await setup();
    view(
      <RatingStatusScope trackIds={[track.id]}>
        <TrackRow />
      </RatingStatusScope>,
    );
    const more = screen.getByRole("button", { name: "更多操作" });
    await screen.findByTestId("ratings-ready");
    await user.click(more);
    await user.click(await screen.findByRole("menuitem", { name: "评分…" }));
    const stars = within(await screen.findByRole("dialog")).getByRole("slider");
    await user.keyboard("{ArrowRight}");
    expect(stars.getAttribute("aria-valuetext")).toBe("1 星，按 Enter 保存");
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(stars.getAttribute("aria-valuetext")).toBe("未评分");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(more);
  });
});
