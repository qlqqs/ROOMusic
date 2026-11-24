import { viewerQueryKey } from "../auth";

export const insightsKeys = {
  status: (generation: number) => viewerQueryKey(generation, "insights", "status"),
  /** 公开的“关于”与登录状态无关，登录页也要读，不挂在 viewer 作用域下。 */
  about: ["public", "about"] as const,
};
