import { describe, expect, it } from "vitest";
import { formatCount } from "./number";

describe("formatCount", () => {
  it("加千位分隔符", () => {
    expect(formatCount(0)).toBe("0");
    expect(formatCount(1234567)).toBe("1,234,567");
  });
});
