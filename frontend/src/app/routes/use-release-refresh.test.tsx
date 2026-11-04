import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { catalogKeys } from "../../features/catalog/query-keys";
import { libraryKeys } from "../../features/library/query-keys";
import { listeningKeys } from "../../features/listening";
import { useReleaseDataRefresh } from "./use-release-refresh";

const dispose: (() => void)[] = [];
afterEach(() => dispose.splice(0).forEach((close) => close()));

describe("认领、撤销与归组后的报告缓存", () => {
  it("当前会话全部扫描报告页随 catalog 与播放历史失效，不污染其他会话或扫描状态", async () => {
    const generation = 7;
    const client = new QueryClient();
    const session = createSessionController({ channel: null });
    dispose.push(() => { client.clear(); session.dispose(); });
    const affected = [
      libraryKeys.scanReportPage(generation, "scan-a", 1, 20),
      libraryKeys.scanReportPage(generation, "scan-a", 2, 20),
      libraryKeys.scanReportPage(generation, "scan-b", 1, 20),
      catalogKeys.group(generation, "album"),
      catalogKeys.recentlyAdded(generation),
      listeningKeys.recentReleases(generation),
    ];
    const unaffected = [
      libraryKeys.scanReportPage(generation + 1, "scan-a", 1, 20),
      catalogKeys.recentlyAdded(generation + 1),
      libraryKeys.scan(generation, "scan-a"),
      libraryKeys.diagnostics(generation, "scan-a"),
    ];
    for (const key of [...affected, ...unaffected]) client.setQueryData(key, { historicalCounts: 3 });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>
        <SessionProvider controller={session}>
          <ViewerScopeProvider viewer={{ id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" }} generation={generation}>
            {children}
          </ViewerScopeProvider>
        </SessionProvider>
      </QueryClientProvider>
    );
    const { result, rerender } = renderHook(() => useReleaseDataRefresh(), { wrapper });
    const refresh = result.current;
    rerender();
    expect(result.current).toBe(refresh);
    await act(async () => { await result.current(); });
    for (const key of affected) {
      expect(client.getQueryState(key)?.isInvalidated).toBe(true);
      // 只标为过期，刷新返回前仍保留历史计数，不能伪造零值。
      expect(client.getQueryData(key)).toEqual({ historicalCounts: 3 });
    }
    for (const key of unaffected) expect(client.getQueryState(key)?.isInvalidated).toBe(false);
  });
});
