import type { ReactNode } from "react";
import { useViewerScope } from "../../features/auth";
import { ForbiddenPage } from "./forbidden-page";

/**
 * 管理页的门：非管理员显示“需要管理员权限”，子内容不渲染，所以管理接口不会被调用。
 * 这只决定显示；后端对每个请求仍然独立鉴权，403 也会按拒绝处理。
 */
export function AdminGate({ children }: { children: ReactNode }) {
  const { viewer } = useViewerScope();
  return viewer.role === "admin" ? <>{children}</> : <ForbiddenPage />;
}
