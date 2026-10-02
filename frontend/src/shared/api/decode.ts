// 运行时解码原语。后端响应以 unknown 进入，只有经过 feature 的 decoder 才成为带类型的值；
// 未知字段被忽略（旧前端可以读新后端），缺字段、错类型、未知枚举与超界一律抛出 DecodeError。
// DecodeError 带字段路径，只用于测试与开发诊断，不展示给用户。

import { isUuid } from "../lib/ids";

export class DecodeError extends Error {
  readonly path: string;

  constructor(path: string, reason: string) {
    super(`${path}: ${reason}`);
    this.name = "DecodeError";
    this.path = path;
  }
}

export type Decoder<T> = (input: unknown, path: string) => T;
export type Infer<D> = D extends Decoder<infer T> ? T : never;

/** 标记“字段可以缺失”的 decoder；只在 object() 的字段上有意义。 */
export type OptionalDecoder<T> = Decoder<T> & { readonly optional: true };

type Shape = Record<string, Decoder<unknown>>;
type OptionalKeys<S extends Shape> = {
  [K in keyof S]: S[K] extends { readonly optional: true } ? K : never;
}[keyof S];
type RequiredKeys<S extends Shape> = Exclude<keyof S, OptionalKeys<S>>;
type Simplify<T> = { [K in keyof T]: T[K] } & {};
export type ObjectOf<S extends Shape> = Simplify<
  { [K in RequiredKeys<S>]: Infer<S[K]> } & { [K in OptionalKeys<S>]?: Infer<S[K]> }
>;

/** 为根响应提供路径起点 `$`，得到 feature 对外导出的 `(input: unknown) => T`。 */
export function defineDecoder<T>(decoder: Decoder<T>): (input: unknown) => T {
  return (input) => decoder(input, "$");
}

function isRecord(input: unknown): input is Record<string, unknown> {
  return typeof input === "object" && input !== null && !Array.isArray(input);
}

export function object<S extends Shape>(shape: S): Decoder<ObjectOf<S>> {
  const keys = Object.keys(shape);
  return (input, path) => {
    if (!isRecord(input)) throw new DecodeError(path, "应为对象");
    const result: Record<string, unknown> = {};
    for (const key of keys) {
      const decoder = shape[key];
      if (decoder === undefined) continue;
      const fieldPath = `${path}.${key}`;
      if (!Object.hasOwn(input, key)) {
        if ("optional" in decoder) continue;
        throw new DecodeError(fieldPath, "缺少字段");
      }
      result[key] = decoder(input[key], fieldPath);
    }
    // 字段已逐个按 shape 解码，这是 ObjectOf 与 Record 之间唯一的类型断言。
    return result as ObjectOf<S>;
  };
}

export function optional<T>(inner: Decoder<T>): OptionalDecoder<T> {
  return Object.assign((input: unknown, path: string) => inner(input, path), { optional: true as const });
}

export function nullable<T>(inner: Decoder<T>): Decoder<T | null> {
  return (input, path) => (input === null ? null : inner(input, path));
}

export function string(options: { min?: number; max?: number; pattern?: RegExp } = {}): Decoder<string> {
  const { min = 0, max = 4096, pattern } = options;
  return (input, path) => {
    if (typeof input !== "string") throw new DecodeError(path, "应为字符串");
    if (input.length < min || input.length > max) throw new DecodeError(path, `长度应在 ${min} 到 ${max} 之间`);
    if (pattern !== undefined && !pattern.test(input)) throw new DecodeError(path, "格式不符");
    return input;
  };
}

export function boolean(): Decoder<boolean> {
  return (input, path) => {
    if (typeof input !== "boolean") throw new DecodeError(path, "应为布尔值");
    return input;
  };
}

export function integer(options: { min?: number; max?: number } = {}): Decoder<number> {
  const { min = Number.MIN_SAFE_INTEGER, max = Number.MAX_SAFE_INTEGER } = options;
  return (input, path) => {
    if (typeof input !== "number" || !Number.isSafeInteger(input)) throw new DecodeError(path, "应为安全整数");
    if (input < min || input > max) throw new DecodeError(path, `应在 ${min} 到 ${max} 之间`);
    return input;
  };
}

/** 有限的数字（允许小数）：时长之类的浮点字段用它；NaN 与无穷大不会出现在合法的 JSON 里，仍然拒绝。 */
export function number(options: { min?: number; max?: number } = {}): Decoder<number> {
  const { min = Number.NEGATIVE_INFINITY, max = Number.POSITIVE_INFINITY } = options;
  return (input, path) => {
    if (typeof input !== "number" || !Number.isFinite(input)) throw new DecodeError(path, "应为有限的数字");
    if (input < min || input > max) throw new DecodeError(path, `应在 ${min} 到 ${max} 之间`);
    return input;
  };
}

// RFC 3339：允许小数秒与时区偏移；Date.parse 对 2 月 30 日之类并不严格，所以先用字段范围约束形状。
const timestampPattern =
  /^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:(?:[0-5]\d|60)(?:\.\d{1,9})?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/;

/** 返回原始字符串：保留原始时刻，格式化只在显示边界做一次。 */
export function timestamp(): Decoder<string> {
  return (input, path) => {
    if (typeof input !== "string" || !timestampPattern.test(input) || Number.isNaN(Date.parse(input))) {
      throw new DecodeError(path, "应为 RFC 3339 时间");
    }
    return input;
  };
}

export function uuid(): Decoder<string> {
  return (input, path) => {
    if (!isUuid(input)) throw new DecodeError(path, "应为 UUID");
    return input;
  };
}

export function literal<const V extends string | number | boolean>(expected: V): Decoder<V> {
  return (input, path) => {
    if (input !== expected) throw new DecodeError(path, `应为 ${String(expected)}`);
    return expected;
  };
}

export function oneOf<const V extends readonly string[]>(values: V): Decoder<V[number]> {
  return (input, path) => {
    const match = values.find((value) => value === input);
    if (match === undefined) throw new DecodeError(path, `不是已知取值（${values.join("、")}）`);
    return match;
  };
}

/** 后端没有给列表设置上限时使用（目录、成员）：显式写出“无上限”，让这一后端缺口留在代码里。 */
export const unbounded = Number.POSITIVE_INFINITY;

/** 先检查长度再逐项映射，超界的数组不会被分配或解码。 */
export function array<T>(item: Decoder<T>, options: { max: number; min?: number }): Decoder<T[]> {
  const { max, min = 0 } = options;
  return (input, path) => {
    if (!Array.isArray(input)) throw new DecodeError(path, "应为数组");
    if (input.length < min || input.length > max) throw new DecodeError(path, `项数应在 ${min} 到 ${max} 之间`);
    return (input as unknown[]).map((value, index) => item(value, `${path}[${index}]`));
  };
}
