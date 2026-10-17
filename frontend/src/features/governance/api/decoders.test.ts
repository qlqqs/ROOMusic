import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import {
  CORRECTABLE_FIELDS,
  decodeCorrectionResult,
  decodeCorrectionState,
  decodeGroupChangeResult,
  decodeGroupDefaultResult,
  decodeIssuePage,
  decodeIssueReviewResult,
  decodeOperationPage,
  decodeReleaseIssues,
  decodeRevertResult,
} from "./decoders";

describe("decodeCorrectionState", () => {
  const initial = sampleBody("corrections/get");
  const corrected = sampleBody("corrections/get-corrected");

  it("解码没有修正的状态：八个字段按固定顺序，修正为 null，有效值等于扫描值", () => {
    const state = decodeCorrectionState(initial);
    expect(state.revision).toBe(1);
    expect(state.fields.map((field) => field.field)).toEqual([...CORRECTABLE_FIELDS]);
    for (const field of state.fields) {
      expect(field.correction).toBeNull();
      expect(field.effective_value).toEqual(field.scan_value);
    }
    expect(state.fields[2]).toMatchObject({ field: "year", scan_value: 1998 });
  });

  it("解码修正后的状态：改成某个值、明确设为空（没有 value）、没有修正三种互不混淆", () => {
    const state = decodeCorrectionState(corrected);
    const byName = Object.fromEntries(state.fields.map((field) => [field.field, field]));
    expect(byName["title"]).toMatchObject({ scan_value: "Sample Symphonies", effective_value: "Sample Symphonies (corrected)", correction: { mode: "set", value: "Sample Symphonies (corrected)" } });
    expect(byName["year"]).toMatchObject({ scan_value: 1998, effective_value: null, correction: { mode: "null" } });
    expect(byName["year"]?.correction).not.toHaveProperty("value");
    expect(byName["genre"]?.correction).toBeNull();
  });

  it.each([
    ["缺少 revision", without(initial, ["revision"])],
    ["revision 为 0", withValue(initial, ["revision"], 0)],
    ["缺少 fields", without(initial, ["fields"])],
    ["字段多于八个", withValue(initial, ["fields"], Array.from({ length: 9 }, () => (initial as { fields: unknown[] }).fields[0]))],
    ["未知字段名", withValue(initial, ["fields", 0, "field"], "source_type")],
    ["release_id 不是 UUID", withValue(initial, ["release_id"], "release-1")],
    ["缺少 uncertain", without(initial, ["fields", 0, "uncertain"])],
    ["uncertain 不是布尔值", withValue(initial, ["fields", 0, "uncertain"], "false")],
    ["缺少 scan_value", without(initial, ["fields", 0, "scan_value"])],
    ["缺少 effective_value", without(initial, ["fields", 0, "effective_value"])],
    ["缺少 correction", without(initial, ["fields", 0, "correction"])],
    ["未知的修正方式", withValue(corrected, ["fields", 0, "correction", "mode"], "clear")],
    ["年份值是小数", withValue(initial, ["fields", 2, "scan_value"], 1998.5)],
    ["年份值超界", withValue(initial, ["fields", 2, "scan_value"], 10000)],
    ["值是布尔值", withValue(initial, ["fields", 3, "scan_value"], true)],
    ["值超过 4096 个字符", withValue(initial, ["fields", 3, "scan_value"], "x".repeat(4097))],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeCorrectionState(input)).toThrow(d.DecodeError);
  });
});

describe("decodeCorrectionResult", () => {
  const patched = sampleBody("corrections/patched");

  it("解码修正成功的响应：新的状态加 operation_id", () => {
    const result = decodeCorrectionResult(patched);
    expect(result.revision).toBe(2);
    expect(result.operation_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(result.fields.filter((field) => field.correction !== null).map((field) => field.field)).toEqual(["title", "year", "label"]);
  });

  it.each([
    ["缺少 operation_id", without(patched, ["operation_id"])],
    ["operation_id 不是 UUID", withValue(patched, ["operation_id"], "op-1")],
    ["缺少 fields", without(patched, ["fields"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeCorrectionResult(input)).toThrow(d.DecodeError);
  });

  it("读取状态的响应没有 operation_id，不能当作修正结果", () => {
    expect(() => decodeCorrectionResult(sampleBody("corrections/get"))).toThrow(d.DecodeError);
  });
});

describe("decodeGroupChangeResult", () => {
  const joined = sampleBody("group-changes/joined");
  const detached = sampleBody("group-changes/detached");

  it("解码归入与移出的响应：现在的组、原来的组、新的版本号和操作 ID", () => {
    const joinedResult = decodeGroupChangeResult(joined);
    const detachedResult = decodeGroupChangeResult(detached);
    expect(joinedResult.revision).toBe(2);
    expect(detachedResult.revision).toBe(3);
    expect(joinedResult.group_id).not.toBe(joinedResult.previous_group_id);
    expect(detachedResult.release_id).toBe(joinedResult.release_id);
  });

  it.each([
    ["缺少 group_id", without(joined, ["group_id"])],
    ["group_id 不是 UUID", withValue(joined, ["group_id"], "group-1")],
    ["缺少 previous_group_id", without(joined, ["previous_group_id"])],
    ["缺少 operation_id", without(joined, ["operation_id"])],
    ["revision 为 0", withValue(joined, ["revision"], 0)],
    ["revision 是字符串", withValue(joined, ["revision"], "2")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeGroupChangeResult(input)).toThrow(d.DecodeError);
  });
});

describe("decodeIssueReviewResult", () => {
  const reviewed = sampleBody("issue-reviews/reviewed");
  const unreviewed = sampleBody("issue-reviews/unreviewed");

  it("解码核对与取消核对的响应：问题、现在是否有核对、新的版本号和操作 ID", () => {
    expect(decodeIssueReviewResult(reviewed)).toMatchObject({ issue: "grouping", reviewed: true, revision: 2 });
    expect(decodeIssueReviewResult(unreviewed)).toMatchObject({ issue: "grouping", reviewed: false, revision: 4 });
  });

  it.each([
    ["缺少 release_id", without(reviewed, ["release_id"])],
    ["不能核对的问题", withValue(reviewed, ["issue"], "title")],
    ["缺少 reviewed", without(reviewed, ["reviewed"])],
    ["reviewed 不是布尔值", withValue(reviewed, ["reviewed"], "true")],
    ["revision 为 0", withValue(reviewed, ["revision"], 0)],
    ["缺少 operation_id", without(reviewed, ["operation_id"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeIssueReviewResult(input)).toThrow(d.DecodeError);
  });
});

describe("decodeIssuePage", () => {
  const page = sampleBody("release-issues/list");
  const reviewed = sampleBody("release-issues/list-reviewed");

  it("解码问题列表：版本摘要、版本说明、待处理与已核对的数目，以及筛选项上的数字", () => {
    const decoded = decodeIssuePage(page);
    expect(decoded.items.map((item) => [item.release.title, item.edition, item.open])).toEqual([
      ["Sample Split Album", "Live", { fields: 1, grouping: 1, missing_tracks: 1 }],
      ["Sample Split Album II", null, { fields: 0, grouping: 1, missing_tracks: 0 }],
    ]);
    for (const item of decoded.items) {
      expect(item.open.fields + item.open.grouping + item.open.missing_tracks).toBe(item.release.attention_count);
    }
    expect(decoded.summary.open).toEqual({ all: 2, fields: 1, grouping: 2, missing_tracks: 1 });
    expect(decodeIssuePage(reviewed).items[0]?.reviewed).toEqual({ fields: 0, grouping: 1, missing_tracks: 1 });
  });

  it.each([
    ["缺少 summary", without(page, ["summary"])],
    ["缺少 reviewed", without(page, ["items", 0, "reviewed"])],
    ["归组数目大于 1", withValue(page, ["items", 0, "open", "grouping"], 2)],
    ["数目是负数", withValue(page, ["items", 0, "open", "fields"], -1)],
    ["group_id 不是 UUID", withValue(page, ["items", 0, "group_id"], "group-1")],
    ["缺少版本摘要", without(page, ["items", 0, "release"])],
    ["筛选项缺少 all", without(page, ["summary", "open", "all"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeIssuePage(input)).toThrow(d.DecodeError);
  });
});

describe("decodeReleaseIssues", () => {
  const issues = sampleBody("release-issues/get");
  const reviewed = sampleBody("release-issues/get-reviewed");

  it("解码版本的问题：字段（只有来源与介质类型可以核对）、归组冲突与同一目录的其他版本、缺失曲目", () => {
    const decoded = decodeReleaseIssues(issues);
    expect(decoded.fields).toEqual([expect.objectContaining({ field: "media_type", value: "CD", candidates: ["CD", "Vinyl"], reviewable: true, status: "open", review: null })]);
    expect(decoded.grouping).toMatchObject({ reason_codes: ["same_directory_conflict"], source_refs: ["split/01.flac", "split/02.flac"], status: "open" });
    expect(decoded.grouping?.siblings.map((sibling) => sibling.title)).toEqual(["Sample Split Album II"]);
    expect(decoded.missing_tracks).toMatchObject({ count: 1, truncated: false, status: "open" });
  });

  it("核对之后：归组冲突已核对并带核对人；之后又有曲目缺失时缺失曲目回到待处理，核对仍在", () => {
    const decoded = decodeReleaseIssues(reviewed);
    expect(decoded.grouping).toMatchObject({ status: "reviewed", review: { reviewer: { username: "curator" } } });
    expect(decoded.missing_tracks).toMatchObject({ count: 2, status: "open", review: { reviewer: { username: "curator" } } });
    expect(decoded.missing_tracks?.tracks.map((track) => track.reviewed)).toEqual([true, false]);
  });

  it("没有归组冲突与缺失曲目时两者为 null", () => {
    const clean = withValue(withValue(withValue(issues, ["grouping"], null), ["missing_tracks"], null), ["fields"], []);
    expect(decodeReleaseIssues(clean)).toMatchObject({ fields: [], grouping: null, missing_tracks: null });
  });

  it.each([
    ["依据令牌长度不对", withValue(issues, ["grouping", "basis"], "abc")],
    ["未知的状态", withValue(issues, ["grouping", "status"], "ignored")],
    ["缺失曲目为空却不是 null", withValue(issues, ["missing_tracks", "tracks"], [])],
    ["曲目缺少最后一次看到的时间", without(issues, ["missing_tracks", "tracks", 0, "last_seen_at"])],
    ["核对缺少核对人", withValue(reviewed, ["grouping", "review", "reviewer"], null)],
    ["同一目录的版本缺少标题", without(issues, ["grouping", "siblings", 0, "title"])],
    ["缺少 revision", without(issues, ["revision"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseIssues(input)).toThrow(d.DecodeError);
  });
});

describe("decodeGroupDefaultResult", () => {
  const set = sampleBody("group-defaults/set");

  it("解码设定的响应：专辑、默认版本、专辑新的版本号和操作 ID", () => {
    expect(decodeGroupDefaultResult(set)).toMatchObject({ revision: 2 });
  });

  it("改回自动选择时 default_release_id 是 null，而不是缺失", () => {
    expect(decodeGroupDefaultResult(withValue(set, ["default_release_id"], null)).default_release_id).toBeNull();
    expect(() => decodeGroupDefaultResult(without(set, ["default_release_id"]))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 group_id", without(set, ["group_id"])],
    ["group_id 不是 UUID", withValue(set, ["group_id"], "group-1")],
    ["default_release_id 不是 UUID", withValue(set, ["default_release_id"], "release-1")],
    ["缺少 operation_id", without(set, ["operation_id"])],
    ["revision 为 0", withValue(set, ["revision"], 0)],
    ["revision 是字符串", withValue(set, ["revision"], "2")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeGroupDefaultResult(input)).toThrow(d.DecodeError);
  });
});

describe("decodeOperationPage", () => {
  const page = sampleBody("operations/list");
  const filtered = sampleBody("operations/list-by-release");
  const groupPage = sampleBody("operations/list-by-group");

  it("解码操作记录：谁、哪张专辑、改了什么、撤销关系与能不能撤销", () => {
    const decoded = decodeOperationPage(page);
    expect(decoded.pagination).toEqual({ page: 1, page_size: 10, total: 4 });
    expect(decoded.items.map((item) => [item.type, item.changes?.kind, item.revertible])).toEqual([
      ["revert", "release_group_change", false],
      ["release_group_change", "release_group_change", false],
      ["release_group_change", "release_group_change", false],
      ["release_correction", "release_correction", true],
    ]);
    const [revert, detach, , correction] = decoded.items;
    expect(revert?.reverts).toBe(detach?.id);
    expect(detach?.reverted_by).toBe(revert?.id);
    expect(revert?.actor.username).toBe("curator");
    expect(correction?.release).toMatchObject({ title: "Sample Symphonies (corrected)", visible: true });
    if (correction?.changes?.kind !== "release_correction") throw new Error("应是字段修正的记录");
    expect(correction.changes.fields.map((field) => field.field)).toEqual(["title", "year", "label"]);
    expect(correction.changes.fields[1]).toMatchObject({ before: { correction: null, effective: 1998 }, after: { correction: { mode: "null" }, effective: null } });
  });

  it("解码按发行筛选的分页", () => {
    expect(decodeOperationPage(filtered).pagination).toMatchObject({ page: 1, page_size: 2, total: 3 });
    expect(decodeOperationPage(filtered).items).toHaveLength(2);
  });

  it("每页最多 100 条", () => {
    const item = (page as { items: unknown[] }).items[0];
    const many = (count: number) => withValue(withValue(page, ["items"], Array.from({ length: count }, () => structuredClone(item))), ["pagination", "page_size"], 100);
    expect(decodeOperationPage(many(100)).items).toHaveLength(100);
    expect(() => decodeOperationPage(many(101))).toThrow(d.DecodeError);
  });

  it("目标是发行的记录 album 为 null；给出 album 时解码标题与可见性", () => {
    expect(decodeOperationPage(page).items.every((item) => item.album === null)).toBe(true);
    const album = { id: "00000000-0000-4000-8000-0000000000aa", title: "某张专辑", visible: false };
    const forAlbum = withValue(withValue(page, ["items", 0, "release"], null), ["items", 0, "album"], album);
    expect(decodeOperationPage(forAlbum).items[0]).toMatchObject({ release: null, album });
  });

  it("解码专辑目标的默认版本记录：album 有值、release 为 null，改动是前后的默认版本快照", () => {
    const [item] = decodeOperationPage(groupPage).items;
    expect(item).toMatchObject({ type: "release_group_default", release: null, album: { title: "Sample Symphonies (corrected)", visible: true }, revertible: true });
    if (item?.changes?.kind !== "release_group_default") throw new Error("应是默认版本的记录");
    expect(item.changes.from.default).toBeNull();
    expect(item.changes.to.default).toMatchObject({ title: "Sample Symphonies (corrected)", year: null, edition: "Remastered" });
    expect(item.changes.to.group_title).toBe("Sample Symphonies (corrected)");
  });

  it("默认版本记录里前后的默认版本可以都是 null（清除自动选择之前就没有指定的记录不会出现，但快照格式允许）", () => {
    const cleared = withValue(groupPage, ["items", 0, "changes", "to", "default"], null);
    expect(decodeOperationPage(cleared).items[0]?.changes).toMatchObject({ kind: "release_group_default", to: { default: null } });
  });

  it("解码核对问题的记录：问题与前后是否有核对，缺失曲目的核对带曲目数，撤销记录的前后相反", () => {
    const items = decodeOperationPage(sampleBody("operations/list-issue-reviews")).items;
    expect(items.map((item) => [item.type, item.changes?.kind === "release_issue_review" ? item.changes.issue : null])).toEqual([
      ["revert", "grouping"],
      ["release_issue_review", "grouping"],
      ["release_issue_review", "missing_tracks"],
      ["release_issue_review", "grouping"],
    ]);
    const [revert, unreview, missing] = items;
    expect(revert?.changes).toMatchObject({ from: { reviewed: false }, to: { reviewed: true } });
    expect(unreview?.changes).toMatchObject({ from: { reviewed: true, track_count: null }, to: { reviewed: false, track_count: null } });
    expect(missing?.changes).toMatchObject({ from: { reviewed: false, track_count: null }, to: { reviewed: true, track_count: 1 } });
  });

  it("目标发行已不存在、没有改动摘要时仍能解码", () => {
    const orphaned = withValue(withValue(page, ["items", 0, "release"], null), ["items", 0, "changes"], null);
    expect(decodeOperationPage(orphaned).items[0]).toMatchObject({ release: null, changes: null });
  });

  it.each([
    ["缺少 items", without(page, ["items"])],
    ["未知的记录类型", withValue(page, ["items", 0, "type"], "release_merge")],
    ["未知的改动类型", withValue(page, ["items", 0, "changes", "kind"], "release_merge")],
    ["缺少改动类型", without(page, ["items", 0, "changes", "kind"])],
    ["id 不是 UUID", withValue(page, ["items", 0, "id"], "op-1")],
    ["created_at 不是 RFC 3339", withValue(page, ["items", 0, "created_at"], "昨天")],
    ["缺少操作者", without(page, ["items", 0, "actor"])],
    ["缺少 revertible", without(page, ["items", 0, "revertible"])],
    ["revertible 不是布尔值", withValue(page, ["items", 0, "revertible"], "true")],
    ["reverts 不是 UUID", withValue(page, ["items", 0, "reverts"], "op-1")],
    ["发行缺少 visible", without(page, ["items", 3, "release", "visible"])],
    ["缺少 album", without(page, ["items", 0, "album"])],
    ["album 缺少 visible", withValue(page, ["items", 0, "album"], { id: "00000000-0000-4000-8000-0000000000aa", title: "某张专辑" })],
    ["album 的 id 不是 UUID", withValue(page, ["items", 0, "album"], { id: "album-1", title: "某张专辑", visible: true })],
    ["修正字段名未知", withValue(page, ["items", 3, "changes", "fields", 0, "field"], "source_type")],
    ["归组快照缺少组 ID", without(page, ["items", 0, "changes", "from", "group_id"])],
    ["默认版本快照缺少版本 ID", without(groupPage, ["items", 0, "changes", "to", "default", "release_id"])],
    ["核对问题的问题未知", withValue(sampleBody("operations/list-issue-reviews"), ["items", 0, "changes", "issue"], "title")],
    ["核对问题缺少前态", without(sampleBody("operations/list-issue-reviews"), ["items", 0, "changes", "from"])],
    ["核对问题的曲目数是负数", withValue(sampleBody("operations/list-issue-reviews"), ["items", 2, "changes", "to", "track_count"], -1)],
    ["默认版本快照的版本 ID 不是 UUID", withValue(groupPage, ["items", 0, "changes", "to", "default", "release_id"], "release-1")],
    ["默认版本快照缺少标题", without(groupPage, ["items", 0, "changes", "to", "default", "title"])],
    ["默认版本记录缺少专辑标题", without(groupPage, ["items", 0, "changes", "from", "group_title"])],
    ["默认版本记录缺少 default 字段", without(groupPage, ["items", 0, "changes", "from", "default"])],
    ["缺少 pagination", without(page, ["pagination"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeOperationPage(input)).toThrow(d.DecodeError);
  });
});

describe("decodeRevertResult", () => {
  const reverted = sampleBody("operations/reverted");

  it("解码撤销成功的响应", () => {
    const result = decodeRevertResult(reverted);
    expect(result.revision).toBe(4);
    expect(result.target_type).toBe("release");
    expect(result.reverts_operation_id).not.toBe(result.operation_id);
  });

  it("解码专辑目标的撤销响应（默认版本的撤销）：目标是专辑，版本号是专辑自己的", () => {
    const result = decodeRevertResult(sampleBody("operations/reverted-default"));
    expect(result).toMatchObject({ target_type: "release_group", revision: 3 });
    expect(decodeRevertResult(withValue(reverted, ["target_type"], "release_group")).target_type).toBe("release_group");
  });

  it.each([
    ["缺少 reverts_operation_id", without(reverted, ["reverts_operation_id"])],
    ["缺少 target_id", without(reverted, ["target_id"])],
    ["target_id 不是 UUID", withValue(reverted, ["target_id"], "release-1")],
    ["缺少 target_type", without(reverted, ["target_type"])],
    ["未知的 target_type", withValue(reverted, ["target_type"], "track")],
    ["revision 为 0", withValue(reverted, ["revision"], 0)],
    ["缺少 operation_id", without(reverted, ["operation_id"])],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeRevertResult(input)).toThrow(d.DecodeError);
  });
});
