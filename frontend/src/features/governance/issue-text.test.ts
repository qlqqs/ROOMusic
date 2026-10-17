import { describe, expect, it } from "vitest";
import { issueCountsText } from "./issue-text";

describe("issueCountsText", () => {
  it("只写有的类别，字段与缺失曲目带数目，归组冲突每个版本至多一个", () => {
    expect(issueCountsText({ fields: 2, grouping: 1, missing_tracks: 3 })).toBe("2 个字段 · 归组冲突 · 3 首缺失曲目");
    expect(issueCountsText({ fields: 0, grouping: 1, missing_tracks: 0 })).toBe("归组冲突");
    expect(issueCountsText({ fields: 1, grouping: 0, missing_tracks: 0 })).toBe("1 个字段");
    expect(issueCountsText({ fields: 0, grouping: 0, missing_tracks: 0 })).toBe("");
  });
});
