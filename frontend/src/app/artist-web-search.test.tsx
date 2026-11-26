import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { artistsKeys } from "../features/artists/query-keys";
import { decodeArtistWebQueue } from "../features/curation/api/decoders";
import { curationKeys } from "../features/curation/query-keys";
import { formatDateTime } from "../shared/lib/time";
import { sampleBody } from "../test/api-samples";
import { artistDataOf, primaryArtistId } from "../test/artist-data";
import { artistImage } from "../test/artist-media-data";
import { artistWebSearchOf } from "../test/artist-web-search-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

beforeEach(() => {
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
});

const id = primaryArtistId();
const detailPath = `/artists/${id}`;
const searchPath = `/api/v1/artists/${id}/web-search`;
const queuePath = "/api/v1/admin/curation/artist-web-search";
const lookupName = "联网查找这位艺人的身份";
const imageSeal = "图片来源：Wikimedia Commons · CC BY 4.0";
const idle = decodeArtistWebQueue(sampleBody("curation/artist-web-search/queue-idle"));
const configuredIdle = { ...idle, configured: true, can_start: true };

function setup(viewer: "curator" | "listener" = "curator") {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: viewer });
  const data = installArtistsApi(api, artistDataOf(), session);
  const detail = data.details.get(id);
  if (detail === undefined) throw new Error("缺少艺人详情样本");
  detail.artist.image = null;
  detail.image = null;
  return { api, detail, user: userEvent.setup() };
}

describe("艺人页的联网查找", () => {
  it.each([
    ["queued", "排队等待联网查找（第 2 位，共 7 位）"],
    ["running", "正在联网查找"],
  ] as const)("从指定对应启动，响应为 %s 时更新印章", async (state, seal) => {
    const { api, user } = setup();
    api.on("GET", searchPath, () => jsonResponse(200, artistWebSearchOf({ configured: true, can_start: true })));
    api.on("POST", searchPath, () => jsonResponse(202, artistWebSearchOf({
      configured: true, can_stop: true, state, queue: { generation: 1, position: 2, total: 7 },
    })));
    renderApp({ path: detailPath });

    await user.click(await screen.findByRole("button", { name: "指定对应" }));
    const dialog = within(screen.getByRole("dialog", { name: "指定对应" }));
    const lookup = await dialog.findByRole("button", { name: lookupName });
    expect(lookup.getAttribute("aria-disabled")).toBeNull();
    expect(api.requestsTo("POST", searchPath)).toHaveLength(0);

    await user.click(lookup);

    expect(await screen.findByRole("button", { name: seal })).toBeTruthy();
    expect(api.requestsTo("POST", searchPath)).toMatchObject([{ body: undefined }]);
  });

  it("从图片来源启动后，停用浮层里的联网按钮并更新查找印章", async () => {
    const { api, detail, user } = setup();
    detail.image = artistImage;
    api.on("GET", searchPath, () => jsonResponse(200, artistWebSearchOf({ configured: true, can_start: true })));
    api.on("POST", searchPath, () => jsonResponse(202, artistWebSearchOf({
      configured: true, can_stop: true, state: "running",
    })));
    const { runtime } = renderApp({ path: detailPath });

    await user.click(await screen.findByRole("button", { name: imageSeal }));
    const dialog = within(screen.getByRole("dialog", { name: "图片来源" }));
    const lookup = await dialog.findByRole("button", { name: lookupName });
    expect(lookup.getAttribute("aria-disabled")).toBeNull();
    expect(api.requestsTo("POST", searchPath)).toHaveLength(0);

    await user.click(lookup);
    const key = artistsKeys.webSearch(runtime.session.currentGeneration(), id);
    await waitFor(() => expect(runtime.queryClient.getQueryData(key)).toMatchObject({ data: { state: "running" } }));
    await waitFor(() => expect(lookup.getAttribute("aria-disabled")).toBe("true"));
    await user.click(lookup);
    expect(api.requestsTo("POST", searchPath)).toMatchObject([{ body: undefined }]);
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "正在联网查找" })).toBeTruthy();
  });

  it.each([
    { dialog: "指定对应", seal: "指定对应", image: null },
    { dialog: "图片来源", seal: imageSeal, image: artistImage },
  ])("配置未就绪时，$dialog 浮层无联网入口", async ({ dialog, seal, image }) => {
    const { api, detail, user } = setup();
    detail.image = image;
    api.on("GET", searchPath, () => jsonResponse(200, artistWebSearchOf()));
    const { runtime } = renderApp({ path: detailPath });

    await user.click(await screen.findByRole("button", { name: seal }));
    const key = artistsKeys.webSearch(runtime.session.currentGeneration(), id);
    await waitFor(() => expect(runtime.queryClient.getQueryState(key)?.status).toBe("success"));
    expect(within(screen.getByRole("dialog", { name: dialog })).queryByRole("button", { name: lookupName })).toBeNull();
    expect(api.requestsTo("POST", searchPath)).toHaveLength(0);
  });

  it.each([
    ["error-model-not-configured", "还没有配好整理用的模型，到设置里配好后再查。"],
    ["error-search-unconfigured", "管家设置里还没有配网页搜索，配好后再查。"],
    ["error-source-disabled", "MusicBrainz 已关闭，联网查找要用它核对。"],
  ] as const)("配置变化后点击联网，显示 %s 的提示", async (sample, message) => {
    const { api, user } = setup();
    api.on("GET", searchPath, () => jsonResponse(200, artistWebSearchOf({ configured: true, can_start: true })));
    api.on("POST", searchPath, () => sampleResponse(`curation/artist-web-search/${sample}`));
    renderApp({ path: detailPath });

    await user.click(await screen.findByRole("button", { name: "指定对应" }));
    const dialog = within(screen.getByRole("dialog", { name: "指定对应" }));
    await user.click(await dialog.findByRole("button", { name: lookupName }));

    expect(await screen.findByText(message, { exact: true })).toBeTruthy();
    expect(api.requestsTo("POST", searchPath)).toHaveLength(1);
    expect(screen.queryByRole("button", { name: /^(排队等待联网查找|正在联网查找)/ })).toBeNull();
  });
});

describe("艺人列表的整理队列", () => {
  it("从标题启动，显示进度，可停止并接续同一队列", async () => {
    const { api, user } = setup();
    const running = {
      ...configuredIdle, state: "running", generation: 4, processed: 2, total: 7, queued: 4, running: 1, can_stop: true,
    };
    api.on("GET", queuePath, () => jsonResponse(200, configuredIdle));
    api.on("POST", queuePath, () => jsonResponse(202, running));
    api.on("DELETE", queuePath, () => jsonResponse(200, {
      ...running, state: "stopped", queued: 5, running: 0, can_stop: false,
    }));
    renderApp({ path: "/artists" });

    const heading = await screen.findByRole("heading", { name: "艺人", level: 1 });
    const header = heading.closest("header");
    if (header === null) throw new Error("缺少艺人列表页头");
    const actions = within(header);
    const start = await actions.findByRole("button", { name: "整理艺人" });
    expect(start.hasAttribute("disabled")).toBe(false);
    expect(api.requestsTo("POST", queuePath)).toHaveLength(0);
    await user.click(start);

    const stop = await actions.findByRole("button", { name: "停止整理艺人" });
    expect(actions.getByRole("status").textContent).toBe("2/7");
    expect(api.requestsTo("POST", queuePath)).toMatchObject([{ body: undefined }]);
    await user.click(stop);

    const resume = await actions.findByRole("button", { name: "接着整理艺人" });
    expect(actions.getByRole("status").textContent).toBe("已停在 2/7");
    expect(api.requestsTo("DELETE", queuePath)).toMatchObject([{ body: undefined }]);
    await user.click(resume);

    expect(await actions.findByRole("button", { name: "停止整理艺人" })).toBeTruthy();
    expect(api.requestsTo("POST", queuePath)).toHaveLength(2);
  });

  it("配置未就绪时隐藏整理入口", async () => {
    const { api } = setup();
    api.on("GET", queuePath, () => sampleResponse("curation/artist-web-search/queue-idle"));
    const { runtime } = renderApp({ path: "/artists" });

    await screen.findByRole("heading", { name: "艺人", level: 1 });
    const key = curationKeys.artistWebQueue(runtime.session.currentGeneration());
    await waitFor(() => expect(runtime.queryClient.getQueryState(key)?.status).toBe("success"));
    expect(screen.queryByRole("button", { name: /整理艺人/ })).toBeNull();
    expect(api.requestsTo("POST", queuePath)).toHaveLength(0);
  });

  it("已配置但 can_start 为 false 时停用按钮", async () => {
    const { api, user } = setup();
    api.on("GET", queuePath, () => jsonResponse(200, { ...configuredIdle, can_start: false }));
    renderApp({ path: "/artists" });

    const button = await screen.findByRole("button", { name: "整理艺人" });
    expect(button.hasAttribute("disabled")).toBe(true);
    await user.click(button);
    expect(api.requestsTo("POST", queuePath)).toHaveLength(0);
  });

  it("日额度用完时显示下次继续的时间和停止按钮", async () => {
    const { api } = setup();
    const retryAt = "2026-12-01T00:00:00Z";
    api.on("GET", queuePath, () => jsonResponse(200, {
      ...configuredIdle, state: "running", generation: 4, total: 7, queued: 5, processed: 2, can_stop: true, retry_at: retryAt,
    }));
    renderApp({ path: "/artists" });

    expect(await screen.findByText(`额度用完，${formatDateTime(retryAt)} 继续`, { exact: true })).toBeTruthy();
    expect(screen.getByRole("button", { name: "停止整理艺人" })).toBeTruthy();
  });

  it("普通成员可以浏览艺人，页面不读取或显示整理队列", async () => {
    const { api, detail } = setup("listener");
    api.on("GET", queuePath, () => jsonResponse(200, configuredIdle));
    renderApp({ path: "/artists" });

    expect(await screen.findByRole("link", { name: detail.artist.name })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /整理艺人/ })).toBeNull();
    expect(api.requestsTo("GET", queuePath)).toHaveLength(0);
  });
});
