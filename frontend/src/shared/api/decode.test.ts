import { describe, expect, it } from "vitest";
import {
  array,
  boolean,
  defineDecoder,
  DecodeError,
  integer,
  literal,
  nullable,
  number,
  object,
  oneOf,
  optional,
  string,
  timestamp,
  unbounded,
  uuid,
} from "./decode";

function decodeFailure(run: () => unknown): DecodeError {
  try {
    run();
  } catch (error) {
    if (error instanceof DecodeError) return error;
    throw error;
  }
  throw new Error("预期抛出 DecodeError");
}

describe("string", () => {
  const decode = defineDecoder(string({ min: 1, max: 5 }));

  it("接受范围内的字符串", () => {
    expect(decode("abc")).toBe("abc");
  });

  it("拒绝非字符串、过短与过长", () => {
    expect(() => decode(1)).toThrow(DecodeError);
    expect(() => decode("")).toThrow(DecodeError);
    expect(() => decode("abcdef")).toThrow(DecodeError);
  });

  it("默认上限为 4096，并校验 pattern", () => {
    expect(() => defineDecoder(string())("a".repeat(4097))).toThrow(DecodeError);
    expect(defineDecoder(string())("a".repeat(4096))).toHaveLength(4096);
    expect(() => defineDecoder(string({ pattern: /^[a-z]+$/ }))("ABC")).toThrow(DecodeError);
  });
});

describe("number", () => {
  it("接受上下界内的有限数字，包含小数", () => {
    const decode = defineDecoder(number({ min: 0, max: 10 }));
    expect(decode(0)).toBe(0);
    expect(decode(3.25)).toBe(3.25);
    expect(decode(10)).toBe(10);
  });

  it("拒绝越界、非数字、字符串形式的数字与非有限值", () => {
    const decode = defineDecoder(number({ min: 0, max: 10 }));
    expect(() => decode(-0.001)).toThrow(DecodeError);
    expect(() => decode(10.001)).toThrow(DecodeError);
    expect(() => decode("3")).toThrow(DecodeError);
    expect(() => decode(null)).toThrow(DecodeError);
    expect(() => decode(Number.NaN)).toThrow(DecodeError);
    expect(() => decode(Number.POSITIVE_INFINITY)).toThrow(DecodeError);
  });
});

describe("boolean 与 integer", () => {
  it("boolean 只接受布尔值", () => {
    expect(defineDecoder(boolean())(true)).toBe(true);
    expect(() => defineDecoder(boolean())("true")).toThrow(DecodeError);
  });

  it("integer 只接受上下界内的安全整数", () => {
    const decode = defineDecoder(integer({ min: 0, max: 100 }));
    expect(decode(0)).toBe(0);
    expect(decode(100)).toBe(100);
    expect(() => decode(101)).toThrow(DecodeError);
    expect(() => decode(-1)).toThrow(DecodeError);
    expect(() => decode(1.5)).toThrow(DecodeError);
    expect(() => decode("1")).toThrow(DecodeError);
    expect(() => defineDecoder(integer())(Number.MAX_SAFE_INTEGER + 1)).toThrow(DecodeError);
  });
});

describe("timestamp", () => {
  const decode = defineDecoder(timestamp());

  it("接受带小数秒与时区偏移的 RFC 3339 时间并保留原字符串", () => {
    expect(decode("2026-01-01T00:00:00Z")).toBe("2026-01-01T00:00:00Z");
    expect(decode("2026-03-09T12:30:45.123456789+08:00")).toBe("2026-03-09T12:30:45.123456789+08:00");
  });

  it("拒绝缺少时区、日期超界与非时间字符串", () => {
    expect(() => decode("2026-01-01T00:00:00")).toThrow(DecodeError);
    expect(() => decode("2026-13-01T00:00:00Z")).toThrow(DecodeError);
    expect(() => decode("2026-01-32T00:00:00Z")).toThrow(DecodeError);
    expect(() => decode("2026-01-01T24:00:00Z")).toThrow(DecodeError);
    expect(() => decode("昨天")).toThrow(DecodeError);
    expect(() => decode(1767225600)).toThrow(DecodeError);
  });
});

describe("uuid", () => {
  it("接受标准 UUID，拒绝其他字符串", () => {
    const decode = defineDecoder(uuid());
    expect(decode("123e4567-e89b-42d3-a456-426614174000")).toBe("123e4567-e89b-42d3-a456-426614174000");
    expect(() => decode("123e4567e89b42d3a456426614174000")).toThrow(DecodeError);
    expect(() => decode("not-a-uuid")).toThrow(DecodeError);
  });
});

describe("oneOf", () => {
  const decode = defineDecoder(oneOf(["admin", "user"]));

  it("只接受列出的取值", () => {
    expect(decode("admin")).toBe("admin");
    expect(() => decode("owner")).toThrow(DecodeError);
    expect(() => decode(1)).toThrow(DecodeError);
  });
});

describe("literal", () => {
  it("只接受指定的值", () => {
    const decode = defineDecoder(literal(true));
    expect(decode(true)).toBe(true);
    expect(() => decode(false)).toThrow(DecodeError);
    expect(() => decode("true")).toThrow(DecodeError);
    expect(() => defineDecoder(literal("a"))("b")).toThrow(DecodeError);
  });
});

describe("array", () => {
  it("逐项解码并在错误路径中带索引", () => {
    const decode = defineDecoder(array(string({ min: 1 }), { max: 3 }));
    expect(decode(["a", "b"])).toEqual(["a", "b"]);
    expect(decodeFailure(() => decode(["a", ""])).path).toBe("$[1]");
  });

  it("超出上限时不解码任何一项", () => {
    let decoded = 0;
    const counting = (input: unknown, path: string) => {
      decoded += 1;
      return string()(input, path);
    };
    const decode = defineDecoder(array(counting, { max: 2 }));
    expect(() => decode(["a", "b", "c"])).toThrow(DecodeError);
    expect(decoded).toBe(0);
  });

  it("unbounded 表示后端没有给出上限，数组长度不受限制", () => {
    const decode = defineDecoder(array(integer(), { max: unbounded }));
    expect(decode(Array.from({ length: 5000 }, (_, index) => index))).toHaveLength(5000);
  });

  it("拒绝非数组与不足最少项数", () => {
    expect(() => defineDecoder(array(string(), { max: 2 }))({})).toThrow(DecodeError);
    expect(() => defineDecoder(array(string(), { min: 1, max: 2 }))([])).toThrow(DecodeError);
  });
});

describe("nullable 与 optional", () => {
  it("nullable 接受 null，否则交给内部 decoder", () => {
    const decode = defineDecoder(nullable(string()));
    expect(decode(null)).toBeNull();
    expect(decode("x")).toBe("x");
    expect(() => decode(1)).toThrow(DecodeError);
  });

  const decode = defineDecoder(object({ name: string(), note: optional(string({ max: 3 })) }));

  it("optional 字段缺失时不出现在结果里，出现时仍要校验", () => {
    const withoutNote = decode({ name: "a" });
    expect(Object.hasOwn(withoutNote, "note")).toBe(false);
    expect(decode({ name: "a", note: "ok" })).toEqual({ name: "a", note: "ok" });
    expect(() => decode({ name: "a", note: "toolong" })).toThrow(DecodeError);
    expect(() => decode({ name: "a", note: null })).toThrow(DecodeError);
  });
});

describe("object", () => {
  const decode = defineDecoder(
    object({
      id: uuid(),
      owner: object({ name: string({ min: 1 }), tags: array(string(), { max: 2 }) }),
      role: oneOf(["admin", "user"]),
    }),
  );
  const valid = {
    id: "123e4567-e89b-42d3-a456-426614174000",
    owner: { name: "curator", tags: ["x"] },
    role: "admin",
  };

  it("解码嵌套对象并忽略未知字段", () => {
    expect(decode({ ...valid, future_field: 1 })).toEqual(valid);
  });

  it("缺字段、错类型与未知枚举都带字段路径", () => {
    expect(decodeFailure(() => decode({ ...valid, id: undefined })).path).toBe("$.id");
    expect(decodeFailure(() => decode({ ...valid, owner: { name: "", tags: [] } })).path).toBe("$.owner.name");
    expect(decodeFailure(() => decode({ ...valid, owner: { name: "a", tags: ["1", "2", "3"] } })).path).toBe(
      "$.owner.tags",
    );
    expect(decodeFailure(() => decode({ ...valid, role: "owner" })).path).toBe("$.role");
  });

  it("拒绝非对象输入", () => {
    for (const input of [null, [], "x", 1, undefined]) {
      expect(() => decode(input)).toThrow(DecodeError);
    }
  });
});
