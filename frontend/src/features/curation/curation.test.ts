import { describe, expect, it } from "vitest";
import type { CurationExceptionDTO, CurationProgressDTO, CurationSettingsDTO, SuppressionDTO } from "./api/decoders";
import { exceptionChangeLines, isPairedIdentityMarker, skippedReason, skippedSummary, suppressionSubjectText, suppressionValueText } from "./curation-text";
import { dismissHint, hintDismissed, shouldShowFirstScanHint } from "./first-scan-hint";
import { progressRefetchInterval } from "./hooks/use-curation";
import { idleQueues } from "../../test/curation-data";

const settings = (patch: Partial<CurationSettingsDTO> = {}): CurationSettingsDTO => ({
  active: true, enabled: true, enabled_at: "2026-01-01T00:00:00Z", enabled_by: { id: "00000000-0000-4000-8000-000000000001", username: "curator" }, inactive_reason: null, paused: false, model: "", daily_token_limit: 10_000_000, ...patch,
});
const progress = (state: CurationProgressDTO["state"], total = 3): CurationProgressDTO => ({
  batch: null, last_pass_finished_at: null, model: { daily_token_limit: null, items: { in_progress: 0, shelved: 0, waiting_human: 0, waiting_model: 0 }, model: "", state: "disabled", today: { date: "2026-01-01", input_tokens: 0, output_tokens: 0 }, unavailable_reason: null }, pending: { waiting_model: 0 }, queues: idleQueues(), releases: { total, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0, unlisted: 0, no_usable_candidates: 0 }, rule_version: 101, state,
});

describe("进度的轮询", () => {
  it("工作或等扫描时 10 秒；设置与进度没对上时 10 秒直到对上；开着且空闲 60 秒；其余不轮询", () => {
    expect(progressRefetchInterval(progress("running"), settings())).toBe(10_000);
    expect(progressRefetchInterval(progress("waiting_for_scan"), undefined)).toBe(10_000);
    expect(progressRefetchInterval(progress("disabled"), settings())).toBe(10_000);
    expect(progressRefetchInterval(progress("paused"), settings())).toBe(10_000);
    expect(progressRefetchInterval(progress("idle"), settings({ paused: true }))).toBe(10_000);
    expect(progressRefetchInterval(progress("idle"), settings())).toBe(60_000);
    expect(progressRefetchInterval(progress("disabled"), settings({ enabled: false, active: false, inactive_reason: "disabled" }))).toBe(false);
    expect(progressRefetchInterval(progress("paused"), settings({ paused: true }))).toBe(false);
    expect(progressRefetchInterval(progress("unavailable"), settings())).toBe(false);
    expect(progressRefetchInterval(undefined, settings())).toBe(false);
  });
});

describe("一次性提示", () => {
  const never = settings({ enabled: false, active: false, enabled_at: null, enabled_by: null, inactive_reason: "disabled" });
  it("只对管理员、曲库里有发行、从未打开过、没有关掉过时出现", () => {
    expect(shouldShowFirstScanHint({ isAdmin: true, settings: never, progress: progress("disabled"), dismissed: false })).toBe(true);
    expect(shouldShowFirstScanHint({ isAdmin: false, settings: never, progress: progress("disabled"), dismissed: false })).toBe(false);
    expect(shouldShowFirstScanHint({ isAdmin: true, settings: never, progress: progress("disabled", 0), dismissed: false })).toBe(false);
    expect(shouldShowFirstScanHint({ isAdmin: true, settings: never, progress: progress("disabled"), dismissed: true })).toBe(false);
    // 打开过又关掉：enabled_at 保留，不再提示。
    expect(shouldShowFirstScanHint({ isAdmin: true, settings: settings({ enabled: false, active: false, inactive_reason: "disabled" }), progress: progress("disabled"), dismissed: false })).toBe(false);
  });

  it("本地存储不可用时当作没有关掉过，关掉也不报错", () => {
    const original = Object.getOwnPropertyDescriptor(window, "localStorage");
    Object.defineProperty(window, "localStorage", { configurable: true, get: () => { throw new Error("blocked"); } });
    try {
      expect(hintDismissed("u")).toBe(false);
      expect(() => dismissHint("u")).not.toThrow();
    } finally {
      if (original !== undefined) Object.defineProperty(window, "localStorage", original);
    }
  });
});

describe("文案", () => {
  const marker = (subject: string, value: SuppressionDTO["value"]): SuppressionDTO => ({
    created_at: "2026-01-01T00:00:00Z", created_by: "00000000-0000-4000-8000-000000000001", id: "00000000-0000-4000-8000-000000000002", operation_id: null,
    subject, target_id: "00000000-0000-4000-8000-000000000003", target_type: "release", value, value_hash: "x",
  });
  it("标记的类别与值：字段写值或留空，封面写来源与编号，归组不显示 ID", () => {
    expect([suppressionSubjectText(marker("label", {})), suppressionValueText(marker("label", { mode: "set", value: "Sample Label" }))]).toEqual(["厂牌", "Sample Label"]);
    expect(suppressionValueText(marker("year", { mode: "null" }))).toBe("留空");
    expect(suppressionValueText(marker("artwork", { source: "coverartarchive", external_id: "42" }))).toBe("Cover Art Archive · 42");
    expect([suppressionSubjectText(marker("group_join", {})), suppressionValueText(marker("group_join", { group_id: "g" }))]).toEqual(["归组", "归入另一张专辑"]);
    expect([suppressionSubjectText(marker("release_merge", {})), suppressionValueText(marker("release_merge", { release_id: "r", title: "Quartet Disc Two" }))]).toEqual(["自动合并发行", "与《Quartet Disc Two》合并"]);
    expect(isPairedIdentityMarker(marker("lyrics_source", {}))).toBe(true);
    expect(isPairedIdentityMarker(marker("lyrics", {}))).toBe(false);
  });

  it("并入的例外：写来源并入目标与收藏的去向，令牌不显示，来源上有隐藏内容时说明", () => {
    const exception = {
      tool: "merge_releases",
      preview: {
        changes: [
          { field: "release_merge", before: { id: "s", title: "Disc Two", group_id: "g" }, after: { id: "t", title: "Disc One", group_id: "g" } },
          { field: "merge_token", before: null, after: "token" },
          { field: "favorites_moved", before: null, after: 2 },
          { field: "hidden", before: null, after: { corrections: 1, reviews: 0, default_version: false, external_artwork: false, links: 0, artist_overrides: 0 } },
        ],
      },
    } as unknown as CurationExceptionDTO;
    expect(exceptionChangeLines(exception)).toEqual(["把《Disc Two》整张并入《Disc One》", "2 个专辑收藏转到合并后的专辑", "被并入的发行上的修正等内容在合并期间不生效，撤销之后恢复"]);
  });

  it("跳过的原因归类计数，未知的码有兜底，临时性失败提示可以再试", () => {
    expect(skippedReason("something_new")).toBe("没能撤销");
    expect(skippedReason("database_unavailable")).toBe("暂时没能撤销，可以再试一次");
    expect(skippedSummary([{ code: "revert_conflict" }, { code: "revert_conflict" }, { code: "already_reverted" }])).toEqual(["之后又被改动 2 条", "已经撤销 1 条"]);
  });
});
