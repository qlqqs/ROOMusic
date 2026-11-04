import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../auth";
import { catalogKeys } from "../../catalog/query-keys";
import { decodeScan, type ScanDTO } from "../api/decoders";
import { libraryKeys } from "../query-keys";
import { useScanCatalogRefresh } from "./use-scan-catalog-refresh";

const generation = 7;
const scanSample = (name: string) => decodeScan(sampleBody(`scan-feedback/${name}`));
const dispose: (() => void)[] = [];
afterEach(() => dispose.splice(0).forEach((close) => close()));

function setup(initial: ScanDTO | null | undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const session = createSessionController({ channel: null });
  dispose.push(() => { client.clear(); session.dispose(); });
  const keys = [
    catalogKeys.albumPage(generation, {}),
    catalogKeys.albumPage(generation, { sort: "added_at_desc", page: 2 }),
    catalogKeys.group(generation, "group"),
    catalogKeys.release(generation, "release"),
    catalogKeys.totals(generation),
    catalogKeys.recentlyAdded(generation),
  ];
  const untouchedKeys = [catalogKeys.recentlyAdded(generation + 1), libraryKeys.roots(generation), libraryKeys.scanReports(generation)];
  for (const key of [...keys, ...untouchedKeys]) client.setQueryData(key, { marker: "已缓存" });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <SessionProvider controller={session}>
        <ViewerScopeProvider generation={generation} viewer={{ id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" }}>
          {children}
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>
  );
  const hook = renderHook(({ scan }: { scan: ScanDTO | null | undefined }) => useScanCatalogRefresh(scan), { initialProps: { scan: initial }, wrapper });
  const expectInvalidated = (expected: boolean) => {
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(expected);
    for (const key of untouchedKeys) expect(client.getQueryState(key)?.isInvalidated).toBe(false);
  };
  return { ...hook, invalidate, expectInvalidated };
}

function processingRoot(index: number, phase: "counting" | "reading_tags" | "saving" = "reading_tags"): ScanDTO {
  const scan = scanSample(`${phase}-admin`);
  return { ...scan, root_progress: { index, total: 5 } };
}

describe("扫描提交边界的 catalog 缓存失效", () => {
  it.each(["counting-admin", "reading_tags-admin", "terminal-succeeded", "terminal-failed"])("首次看到 %s 只记录状态，不失效", (name) => {
    const { invalidate, expectInvalidated } = setup(scanSample(name));
    expect(invalidate).not.toHaveBeenCalled();
    expectInvalidated(false);
  });

  it("首次加载 undefined→null→活动扫描不触发刷新", () => {
    const { rerender, invalidate, expectInvalidated } = setup(undefined);
    rerender({ scan: null });
    rerender({ scan: scanSample("reading_tags-admin") });
    expect(invalidate).not.toHaveBeenCalled();
    expectInvalidated(false);
  });

  it.each(["succeeded", "failed", "canceled", "incomplete"] as const)("running→%s 让列表、详情、数字、最近新增同时失效且仅一次", (status) => {
    const scan = scanSample("reading_tags-admin");
    const { rerender, invalidate, expectInvalidated } = setup(scan);
    const terminal = { ...scan, status, finished_at: "2026-01-01T00:00:03Z" };
    rerender({ scan: terminal });
    expectInvalidated(true);
    expect(invalidate).toHaveBeenCalledExactlyOnceWith({ queryKey: catalogKeys.all(generation) });
    rerender({ scan: { ...terminal } });
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("已知活动 id 消失后失效，不要求知道成功还是非成功", () => {
    const { rerender, invalidate, expectInvalidated } = setup(scanSample("reading_tags-admin"));
    rerender({ scan: null });
    expectInvalidated(true);
    expect(invalidate).toHaveBeenCalledTimes(1);
    rerender({ scan: null });
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("活动 id 换成新扫描，即使新扫描还在 counting 也失效一次", () => {
    const { rerender, invalidate, expectInvalidated } = setup(scanSample("reading_tags-admin"));
    const scan = scanSample("counting-admin");
    const id = "00000000-0000-4000-8000-000000000099";
    rerender({ scan: { ...scan, id, scan_run_id: id } });
    expectInvalidated(true);
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it.each([2, 4])("正式处理从根 1 换到根 %s 时一次失效，跨过多个根也不重复", (index) => {
    const { rerender, invalidate, expectInvalidated } = setup(processingRoot(1, "saving"));
    rerender({ scan: processingRoot(index) });
    expectInvalidated(true);
    expect(invalidate).toHaveBeenCalledTimes(1);
    rerender({ scan: processingRoot(index, "saving") });
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("counting 的根切换及 counting→正式处理都不误认为已有目录提交", () => {
    const { rerender, invalidate, expectInvalidated } = setup(processingRoot(1, "counting"));
    rerender({ scan: processingRoot(2, "counting") });
    rerender({ scan: processingRoot(1) });
    expect(invalidate).not.toHaveBeenCalled();
    expectInvalidated(false);
  });

  it("同根更新或阶段切换不失效，全根保存要等终态再刷新", () => {
    const { rerender, invalidate, expectInvalidated } = setup(processingRoot(2));
    rerender({ scan: processingRoot(2, "saving") });
    const saving = processingRoot(2, "saving");
    rerender({ scan: { ...saving, root_progress: { index: null, total: 5 } } });
    expect(invalidate).not.toHaveBeenCalled();
    expectInvalidated(false);
  });

  it("暂时未读到数据不抹掉先前活动扫描，后续消失仍触发失效", () => {
    const { rerender, invalidate, expectInvalidated } = setup(processingRoot(1));
    rerender({ scan: undefined });
    expect(invalidate).not.toHaveBeenCalled();
    rerender({ scan: null });
    expectInvalidated(true);
    expect(invalidate).toHaveBeenCalledTimes(1);
  });
});
