import { afterEach } from "vitest";
import { albumGroup, groupDetailOf, releaseVersions } from "./catalog-data";
import { claimDetail, suggestionOf } from "./claim-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "./fake-api";
import { installCatalogApi } from "./fake-catalog";
import { installGovernanceApi } from "./fake-governance";
import { seedClaim, type GovernanceData } from "./governance-data";

// 待认领页的测试场景（读取与认领的 Vitest 共用）。

// 两张可见专辑：新版本 Alpha（旧版本 1 的推荐）与 Beta；三个待认领的旧版本：1 最新、2 次之（和 Gamma 同在一张专辑里）、3 最久。
export const alpha = () => albumGroup(1, { title: "Alpha Suites New Rip", artist: "Alpha Cellist", year: 1998 });
export const beta = () => albumGroup(2, { title: "Beta Quartets", artist: "Beta Quartet", year: 1990 });

export const reviewedAt = "2026-01-05T09:30:00Z";

export function seed(governance: GovernanceData) {
  seedClaim(
    governance,
    claimDetail(1, {
      title: "Alpha Suites (corrected)",
      artist: "Alpha Cellist",
      edition: "Remaster",
      directory: "Alpha/Suites",
      lastSeenAt: "2026-01-20T08:00:00Z",
      suggestions: [suggestionOf(alpha(), ["same_files", "same_directory"], 12, "Remaster"), suggestionOf(beta(), ["same_directory"], 0)],
    }),
  );
  seedClaim(
    governance,
    claimDetail(2, {
      title: "Gamma Sonatas",
      artist: "Gamma Duo",
      directory: "Gamma",
      lastSeenAt: "2026-01-10T08:00:00Z",
      corrections: [{ field: "year", correction: { mode: "null" }, scan_value: 2003 }],
      album: {
        id: "00000000-0000-4000-8000-0000000000f1",
        title: "Gamma Sonatas Collection",
        siblings: [
          { id: "00000000-0000-4000-8000-0000000000d1", title: "Gamma Sonatas Collection (live)", visible: true },
          { id: "00000000-0000-4000-8000-0000000000d2", title: "Gamma Sonatas Collection (hidden)", visible: false },
        ],
      },
      defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f1", title: "Gamma Sonatas Collection" }],
      reviews: [
        { issue: "source_type", reviewed_at: reviewedAt, reviewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator" } },
        { issue: "grouping", reviewed_at: reviewedAt, reviewer: { id: "00000000-0000-4000-8000-000000000009", username: "" } },
      ],
    }),
  );
  seedClaim(governance, claimDetail(3, { title: "Delta Partitas", artist: "Delta Player", directory: "", lastSeenAt: "2025-12-01T08:00:00Z" }));
}

// 一张有两个版本的专辑：搜索到它时要再选版本；新版本在这样的专辑里时，旧版本的专辑不再归入（以新版本已有的为准）。
export const delta = () => albumGroup(4, { title: "Delta Concertos", artist: "Delta Ensemble", year: 1985, release_count: 2 });

export function setup(options: { role?: "admin" | "user" } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const catalog = installCatalogApi(api, { groups: [alpha(), beta(), delta()] });
  catalog.groupDetails.set(delta().id, groupDetailOf(delta(), releaseVersions(delta(), 2)));
  const governance = installGovernanceApi(api, catalog);
  seed(governance);
  return { api, catalog, governance };
}


/** jsdom 没有 ResizeObserver：待认领页按宽屏处理。窄屏的测试装一个报告指定宽度的替身，并在每个测试之后还原。 */
const originalResizeObserver = globalThis.ResizeObserver;
afterEach(() => {
  globalThis.ResizeObserver = originalResizeObserver;
});
export function narrowContainer(width: number) {
  globalThis.ResizeObserver = class {
    constructor(private readonly callback: ResizeObserverCallback) {}
    observe(target: Element) {
      target.getBoundingClientRect = () => ({ width, height: 600, x: 0, y: 0, top: 0, left: 0, right: width, bottom: 600, toJSON: () => ({}) }) as DOMRect;
      this.callback([], this as unknown as ResizeObserver);
    }
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}
