import { describe, expect, it } from "vitest";
import { listApiSampleNames, loadApiSample } from "../../test/api-samples";
import { decodeReleaseComparison, decodeReleaseDetail, decodeReleaseEvidence } from "./api/decoders";
import {
  candidateKindLabel,
  codecLabel,
  confidenceLabel,
  creditRoleLabel,
  evidenceActionLabel,
  evidenceFieldLabel,
  evidenceSourceLabel,
  reasonCodeLabel,
} from "./labels";

describe("词表标签", () => {
  const labels = [
    ["署名角色", creditRoleLabel, "composer", "作曲"],
    ["证据字段", evidenceFieldLabel, "catalog_number", "编号"],
    ["证据来源", evidenceSourceLabel, "filename_fallback", "文件名（推断）"],
    ["证据来源", evidenceSourceLabel, "compilation", "合辑"],
    ["可信度", confidenceLabel, "medium", "中"],
    ["处理方式", evidenceActionLabel, "uncertain_apply", "不确定，已暂时采用"],
    ["原因码", reasonCodeLabel, "inconsistent_candidates", "候选值互相不一致"],
    ["归组方式", candidateKindLabel, "box_leaf", "套装里的子目录"],
    ["归组方式", candidateKindLabel, "format_split", "同一目录按格式分开"],
    ["音频编码", codecLabel, "alac", "ALAC"],
  ] as const;

  it.each(labels)("%s：已知值给中文名，未知值原样显示", (_name, label, known, expected) => {
    expect(label(known)).toBe(expected);
    expect(label("some_future_value")).toBe("some_future_value");
    expect(label("")).toBe("");
  });

  it.each([
    ["ape", "APE"],
    ["wavpack", "WavPack"],
    ["wavpack_hybrid", "WavPack（有损）"],
    ["wavpack_float", "WavPack（浮点）"],
    ["wavpack_dsd", "WavPack（DSD）"],
    ["aiff", "AIFF"],
    ["aiff_float", "AIFF（浮点）"],
  ])("新增编码 %s 显示为 %s，不附加派生标记", (codec, expected) => {
    expect(codecLabel(codec)).toBe(expected);
  });

  it("未列出的 AIFF-C 压缩编码保持原值", () => {
    expect(codecLabel("aifc_ima4")).toBe("aifc_ima4");
  });

  it("后端样本里出现过的取值都有中文名（样本之外新增的取值会原样显示）", () => {
    const names = listApiSampleNames().filter((name) => name.startsWith("releases/get") || name === "releases/evidence");
    expect(names.length).toBeGreaterThan(0);
    const unknown: string[] = [];
    const check = (label: (value: string) => string, value: string, kind: string) => {
      if (label(value) === value) unknown.push(`${kind}:${value}`);
    };
    for (const name of names) {
      const body = loadApiSample(name).body;
      if (name === "releases/evidence") {
        const evidence = decodeReleaseEvidence(body);
        for (const field of evidence.fields) {
          check(evidenceFieldLabel, field.field, "field");
          check(evidenceSourceLabel, field.source, "source");
          check(confidenceLabel, field.confidence, "confidence");
          check(evidenceActionLabel, field.action, "action");
        }
        if (evidence.grouping !== null) check(candidateKindLabel, evidence.grouping.candidate_kind, "candidate_kind");
        continue;
      }
      const release = decodeReleaseDetail(body);
      for (const item of release.evidence) {
        check(evidenceFieldLabel, item.field, "field");
        check(evidenceSourceLabel, item.source, "source");
        check(confidenceLabel, item.confidence, "confidence");
        check(evidenceActionLabel, item.action, "action");
      }
      for (const credit of [...release.credits, ...release.media.flatMap((medium) => medium.tracks.flatMap((track) => track.credits))]) {
        check(creditRoleLabel, credit.role, "role");
      }
      for (const track of release.media.flatMap((medium) => medium.tracks)) {
        if (track.codec !== null) check(codecLabel, track.codec, "codec");
      }
    }
    // 版本对比样本里的音频编码。
    for (const release of decodeReleaseComparison(loadApiSample("release-groups/comparison").body).releases) {
      for (const format of release.formats) {
        if (format.codec !== null) check(codecLabel, format.codec, "codec");
      }
    }
    expect(unknown).toEqual([]);
  });
});
