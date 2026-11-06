import { describe, expect, it } from "vitest";
import { formatBytes } from "./bytes";

describe("formatBytes", () => {
  it.each([
    [0, "0 B"], [1, "1 B"], [1023, "1,023 B"], [1024, "1.0 KB"], [1536, "1.5 KB"],
    [10 * 1024, "10 KB"], [320 * 1024, "320 KB"], [1024 ** 2, "1.0 MB"],
    [1.5 * 1024 ** 3, "1.5 GB"], [1024 ** 4, "1.0 TB"], [10 * 1024 ** 4, "10 TB"],
    [9.96 * 1024, "10 KB"], [1023.6 * 1024, "1.0 MB"],
    [-1, "—"], [Number.NaN, "—"], [Number.POSITIVE_INFINITY, "—"], [Number.NEGATIVE_INFINITY, "—"],
  ])("%s → %s", (input, expected) => expect(formatBytes(input)).toBe(expected));
});
