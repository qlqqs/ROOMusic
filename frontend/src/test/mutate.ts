type Path = readonly (string | number)[];

function cloneAt(input: unknown, path: Path): { root: unknown; parent: Record<string, unknown> | unknown[]; key: string | number } {
  const root: unknown = structuredClone(input);
  let current: unknown = root;
  for (const segment of path.slice(0, -1)) {
    current = (current as Record<string | number, unknown>)[segment];
  }
  const key = path[path.length - 1];
  if (typeof current !== "object" || current === null || key === undefined || !(key in current)) {
    throw new Error(`路径不存在：${path.join(".")}`);
  }
  return { root, parent: current as Record<string, unknown> | unknown[], key };
}

/** 返回把 path 处的值改成 value 的副本；路径不存在时抛错，避免反例因路径写错而“偶然通过”。 */
export function withValue(input: unknown, path: Path, value: unknown): unknown {
  const { root, parent, key } = cloneAt(input, path);
  (parent as Record<string | number, unknown>)[key] = value;
  return root;
}

/** 返回删去 path 处字段的副本。 */
export function without(input: unknown, path: Path): unknown {
  const { root, parent, key } = cloneAt(input, path);
  delete (parent as Record<string | number, unknown>)[key];
  return root;
}
