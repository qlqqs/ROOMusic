import type { CurationSourceDTO } from "../api/decoders";

/** 依赖有缺（网络搜索要对话模型与搜索来源）：开关不可用（已经打开的仍可以关掉）；缺什么写在这一行的说明里。 */
export function requirementsMissing(source: CurationSourceDTO): boolean {
  return source.requirements?.some((requirement) => !requirement.ready) ?? false;
}
