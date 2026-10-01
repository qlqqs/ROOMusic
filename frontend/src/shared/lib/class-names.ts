/** 拼接 CSS Modules 的类名，忽略 false、null 与 undefined。 */
export function cx(...parts: readonly (string | false | null | undefined)[]): string {
  return parts.filter((part): part is string => typeof part === "string" && part !== "").join(" ");
}
