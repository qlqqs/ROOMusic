import { Component, Suspense, type ReactNode } from "react";
import { Button, Notice, Skeleton } from "../../shared/ui";

type BoundaryState = { failed: boolean };

/** 按需加载的页面代码块失败（断网、部署后旧代码块 404）时显示错误与刷新，不留空白页。 */
class LazyPageBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  override state: BoundaryState = { failed: false };

  static getDerivedStateFromError(): BoundaryState {
    return { failed: true };
  }

  override render() {
    if (!this.state.failed) return this.props.children;
    // lazyRouteComponent 会记住失败的导入，只有刷新页面才会重新下载代码块。
    return (
      <Notice tone="error" title="页面没能加载" action={<Button onClick={() => window.location.reload()}>刷新页面</Button>}>
        可能是网络中断，或者应用刚刚更新。刷新页面后重试。
      </Notice>
    );
  }
}

/**
 * 管理页与播放设置页按需加载（lazyRouteComponent）的外层：下载期间显示加载占位，失败时显示错误态。
 * 放在 AdminGate 之内，非管理员不会下载这些代码块。
 */
export function LazyPage({ children }: { children: ReactNode }) {
  return (
    <LazyPageBoundary>
      <Suspense fallback={<Skeleton label="正在加载页面" />}>{children}</Suspense>
    </LazyPageBoundary>
  );
}
