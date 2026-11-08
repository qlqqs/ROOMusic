import { describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { playerTrack } from "../../../test/player-data";
import { decodeTrackAnalysis } from "../api/decoders";
import { emptyQueue, type QueueState } from "./queue";
import { chooseLeveling, inAlbumContext, linearGain, UNKNOWN_GAIN_DB } from "./leveling";

const done = decodeTrackAnalysis(sampleBody("tracks/analysis-done"));
const track = { ...done, album: null };
const album = { ...done, album: { releaseId: "release-1", gainDb: 7.8 } };
const entries = (releaseIds: string[], mode: QueueState["mode"] = "sequential"): QueueState => ({
  ...emptyQueue, mode, currentEntryId: "e1", entries: releaseIds.map((releaseId, index) => ({ entryId: `e${index + 1}`, track: playerTrack(String(index + 1), { releaseId }) })),
});

describe("均衡增益选择", () => {
  it.each([
    ["off", null, false, null],
    ["track", track, false, { gainDb: done.analysis?.trackGainDb, basis: "track" }],
    ["album", album, false, { gainDb: 7.8, basis: "album" }],
    ["album", track, false, { gainDb: done.analysis?.trackGainDb, basis: "track" }],
    ["auto", album, true, { gainDb: 7.8, basis: "album" }],
    ["auto", album, false, { gainDb: done.analysis?.trackGainDb, basis: "track" }],
    ["auto", track, true, { gainDb: done.analysis?.trackGainDb, basis: "track" }],
    ["track", null, false, { gainDb: UNKNOWN_GAIN_DB, basis: "unknown" }],
  ] as const)("%s 模式", (mode, analysis, context, expected) => {
    expect(chooseLeveling(mode, analysis, context)).toEqual(expected);
  });

  it("未知增益固定 −5 dB，正增益不截断", () => {
    expect(chooseLeveling("track", null, false)?.gainDb).toBe(-5);
    expect(linearGain(7.8)).toBeCloseTo(10 ** (7.8 / 20));
    expect(chooseLeveling("album", album, true)?.gainDb).toBe(7.8);
  });

  it.each([
    [["release-1", "release-1"], "sequential", true],
    [["release-1", "release-2"], "sequential", false],
    [["release-1", "release-1"], "shuffle", false],
  ] as const)("发行上下文 %j / %s", (ids, mode, expected) => {
    expect(inAlbumContext(entries([...ids], mode), "e1")).toBe(expected);
  });
});
