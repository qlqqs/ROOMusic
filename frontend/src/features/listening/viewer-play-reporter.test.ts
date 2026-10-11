import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it } from "vitest";
import { installFakeApi } from "../../test/fake-api";
import { sampleResponse } from "../../test/http";
import { listeningKeys } from "./query-keys";
import { createViewerPlayReporter } from "./viewer-play-reporter";

const clients: QueryClient[] = [];
afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe("某个会话代次的上报器", () => {
  it("POST /api/v1/me/plays 带 UUID 幂等键与曲目、秒数；成功后只让这个会话代次的“最近播放”查询失效", async () => {
    const api = installFakeApi();
    api.on("POST", "/api/v1/me/plays", () => sampleResponse("listening/play-recorded"));
    const client = new QueryClient();
    clients.push(client);
    const mine = listeningKeys.recentReleases(7);
    const otherSession = listeningKeys.recentReleases(8);
    const unrelated = ["viewer", 7, "catalog", "release-groups"];
    for (const key of [mine, otherSession, unrelated]) client.setQueryData(key, { cached: true });

    const reporter = createViewerPlayReporter(client, 7);
    reporter.report({ trackId: "00000000-0000-4000-8000-000000000002", playedSeconds: 130.5 });
    await new Promise((resolve) => setTimeout(resolve, 20));

    const [request, ...others] = api.requestsTo("POST", "/api/v1/me/plays");
    expect(others).toEqual([]);
    expect(request?.body).toEqual({ track_id: "00000000-0000-4000-8000-000000000002", played_seconds: 130.5 });
    expect(request?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
    expect(client.getQueryState(mine)?.isInvalidated).toBe(true);
    expect(client.getQueryState(otherSession)?.isInvalidated).toBe(false);
    expect(client.getQueryState(unrelated)?.isInvalidated).toBe(false);
  });

  it("上报被拒绝时不让任何查询失效", async () => {
    const api = installFakeApi();
    api.on("POST", "/api/v1/me/plays", () => sampleResponse("errors/play-too-short"));
    const client = new QueryClient();
    clients.push(client);
    const key = listeningKeys.recentReleases(1);
    client.setQueryData(key, { cached: true });

    createViewerPlayReporter(client, 1).report({ trackId: "00000000-0000-4000-8000-000000000002", playedSeconds: 3 });
    await new Promise((resolve) => setTimeout(resolve, 20));

    expect(api.requestsTo("POST", "/api/v1/me/plays")).toHaveLength(1);
    expect(client.getQueryState(key)?.isInvalidated).toBe(false);
  });
});
