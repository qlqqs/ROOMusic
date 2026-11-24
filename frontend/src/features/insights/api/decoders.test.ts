import { describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { decodeAbout, decodeInsightsStatus } from "./decoders";

describe("匿名统计与关于接口解码", () => {
  it("后端样本：开启、关闭与部署层关闭的状态都能解出完整载荷", () => {
    const status = decodeInsightsStatus(sampleBody("insights/status"));
    expect(status.enabled).toBe(true);
    expect(status.notice_acknowledged).toBe(false);
    expect(status.payload.instance_id).toBe(status.instance_id);
    expect(status.payload.library).toStrictEqual({ tracks: "0", releases: "0" });
    expect(decodeInsightsStatus(sampleBody("insights/disabled")).enabled).toBe(false);
  });

  it("载荷只接受约定的分档", () => {
    const body = sampleBody("insights/status") as { payload: { members: string } };
    expect(() => decodeInsightsStatus({ ...body, payload: { ...body.payload, members: "3" } })).toThrow("$.payload.members");
  });

  it("关于：许可与源代码地址，只接受 http(s) 地址", () => {
    expect(decodeAbout(sampleBody("insights/about"))).toStrictEqual({ license: "AGPL-3.0-only", source_url: "https://github.com/qlqqs/ROOMusic" });
    expect(() => decodeAbout({ license: "AGPL-3.0-only", source_url: "javascript:alert(1)" })).toThrow("$.source_url");
  });
});
