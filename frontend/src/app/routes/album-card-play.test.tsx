import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { PlayerProvider } from "../../features/player";
import { albumGroup, releaseDetailOf, mediaOf } from "../../test/catalog-data";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { installCatalogApi } from "../../test/fake-catalog";
import { playerHarness } from "../../test/fake-zones";
import { jsonResponse } from "../../test/http";
import { AlbumCardPlay } from "./album-card-play";
import styles from "./album-card-play.module.css";

const dispose: (() => void)[] = [];
afterEach(() => { for (const cleanup of dispose.splice(0)) cleanup(); });

/** 能播放 FLAC 与 CUE 分轨；样本曲目是 FLAC。 */
const formats = { items: [{ source_kind: "flac_vorbis_comment", codec: "flac" }, { source_kind: "cue_virtual", codec: "flac" }] };

async function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  api.on("GET", "/api/v1/playback/formats", () => jsonResponse(200, formats));
  const group = albumGroup(1, { title: "卡片专辑", track_count: 3 });
  const catalog = installCatalogApi(api, { groups: [group] });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const player = playerHarness();
  dispose.push(() => { player.engine.endSession(); controller.dispose(); client.clear(); });
  render(
    <QueryClientProvider client={client}>
      <SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
          <PlayerProvider userId={state.viewer.id} controller={player.engine}>
            <AlbumCardPlay group={group} />
          </PlayerProvider>
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>,
  );
  const button = () => screen.getByRole("button", { name: "播放《卡片专辑》" });
  await waitFor(() => expect(button().hasAttribute("disabled")).toBe(false));
  return { api, catalog, group, player, button };
}

describe("专辑卡片的播放键", () => {
  it("按下读代表版本、替换队列放整张；“已选择 N 首”只给读屏", async () => {
    const { api, group, player, button } = await setup();
    await userEvent.click(button());
    await player.queued(3);
    expect(api.requestsTo("GET", `/api/v1/releases/${group.representative.id}`)).toHaveLength(1);
    expect(player.engine.getSnapshot().current?.track.groupId).toBe(group.id);
    const status = screen.getByRole("status");
    expect(status.textContent).toBe("已选择 3 首。");
    expect(status.className).toBe(styles.quiet);
  });

  it("跳过这个浏览器放不了的曲目，只放能播放的", async () => {
    const { catalog, group, player, button } = await setup();
    catalog.releases.set(
      group.representative.id,
      releaseDetailOf(group.representative, { media: mediaOf(group.representative, (_medium, track) => (track === 2 ? {} : { source_kind: "m4a", codec: "alac" })) }),
    );
    await userEvent.click(button());
    await player.queued(1);
    expect(player.queueIds()).toEqual([catalog.releases.get(group.representative.id)?.media[0]?.tracks[1]?.id]);
  });

  it("一首都不能播放：看得见的说明，不动当前队列", async () => {
    const { catalog, group, player, button } = await setup();
    catalog.releases.set(group.representative.id, releaseDetailOf(group.representative, { media: mediaOf(group.representative, () => ({ source_kind: "m4a", codec: "alac" })) }));
    await userEvent.click(button());
    expect((await screen.findByText("这张专辑没有能播放的曲目。")).className).toBe(styles.notice);
    expect(player.engine.getSnapshot().queue.entries).toHaveLength(0);
  });

  it("读取失败就地说一句，再按一次就是重试", async () => {
    const { catalog, player, button } = await setup();
    catalog.releaseFailure = jsonResponse(503, { error: { code: "database_unavailable", message: "数据库暂时不可用。", request_id: "req-1" } });
    await userEvent.click(button());
    expect(await screen.findByText("没能读取这张专辑，请重试。")).toBeTruthy();
    expect(player.engine.getSnapshot().queue.entries).toHaveLength(0);
    catalog.releaseFailure = null;
    await userEvent.click(button());
    await player.queued(3);
    await waitFor(() => expect(screen.queryByText("没能读取这张专辑，请重试。")).toBeNull());
  });
});
