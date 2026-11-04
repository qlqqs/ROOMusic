import { afterEach, describe, expect, it, vi } from "vitest";
import { normalizePlaylistName, playlistNameProblem, queuePlaylistName } from "./playlist-name";

afterEach(() => vi.useRealTimers());

describe("歌单名字", () => {
  it.each(["", "   ", "\u3000", String.fromCodePoint(9, 10, 13)])("拒绝规范化后的空名字 %j", (value) => {
    expect(playlistNameProblem(value)).toBe("请输入名字。");
  });

  it("去掉首尾空白，保留名字内的空格与字形", () => {
    const name = "周末  聆听 é 𝄞";
    expect(normalizePlaylistName(`\u3000 ${name} \u3000`)).toBe(name);
    expect(playlistNameProblem(`\u3000 ${name} \u3000`)).toBeNull();
  });

  it.each(["曲", "A", "𝄞", "🎵"])("按 Unicode 码点计数：%s 重复 100 次通过，101 次拒绝", (character) => {
    expect(playlistNameProblem(character.repeat(100))).toBeNull();
    expect(playlistNameProblem(character.repeat(101))).toBe("名字最多 100 个字。");
    expect(playlistNameProblem(`\u3000${character.repeat(100)}\u3000`)).toBeNull();
  });

  it("混合 BMP 与补充平面字符仍按码点计数，不按 UTF-16 长度", () => {
    const hundred = "曲🎵".repeat(50);
    expect(hundred.length).toBe(150);
    expect(playlistNameProblem(hundred)).toBeNull();
    expect(playlistNameProblem(`${hundred}终`)).toBe("名字最多 100 个字。");
  });

  it("组合音符各自占码点，不把字素簇当成单一码点", () => {
    expect(playlistNameProblem("é".repeat(50))).toBeNull();
    expect(playlistNameProblem(`${"é".repeat(50)}a`)).toBe("名字最多 100 个字。");
  });

  const controls = [
    ...Array.from({ length: 0x20 }, (_, code) => code),
    ...Array.from({ length: 0x21 }, (_, index) => 0x7f + index),
  ];
  it.each(controls.map((code) => [code.toString(16).padStart(4, "0"), code] as const))("拒绝名字中的控制字符 U+%s", (_label, code) => {
    // 放在中间，避免首尾空白规范化先去掉待验证字符。
    expect(playlistNameProblem(`前${String.fromCodePoint(code)}后`)).toBe("名字里有不能用的字符。");
  });

  it("普通标点与非控制 Unicode 字符可以作为名字", () => {
    expect(playlistNameProblem("《晚间》— Café · 室内乐 🎻")).toBeNull();
  });
});

describe("队列另存歌单的默认名字", () => {
  it.each([
    [new Date(2026, 0, 2, 0, 5), "队列 2026-01-02"],
    [new Date(2026, 10, 4, 23, 59), "队列 2026-11-04"],
    [new Date(2028, 1, 29, 12), "队列 2028-02-29"],
  ])("使用传入日期的本地年月日：%s", (date, expected) => {
    expect(queuePlaylistName(date)).toBe(expected);
  });

  it("不传日期时使用今天，每次打开都会读取当前日期", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 10, 4, 23, 59, 59));
    expect(queuePlaylistName()).toBe("队列 2026-11-04");
    vi.setSystemTime(new Date(2026, 10, 5, 0, 0, 1));
    expect(queuePlaylistName()).toBe("队列 2026-11-05");
  });
});
