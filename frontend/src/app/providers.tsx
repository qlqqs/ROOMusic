import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { SessionProvider, useSession } from "../features/auth";
import { ServiceUnavailablePage } from "./routes/service-unavailable-page";
import { Splash } from "./routes/splash";
import type { AppRuntime } from "./runtime";

/** 启动确认会话期间显示占位，确认失败显示可重试的整页错误；之后才渲染路由。 */
function AppGate({ runtime }: { runtime: AppRuntime }) {
  const state = useSession();
  if (state.status === "checking") return <Splash />;
  if (state.status === "unavailable") {
    return <ServiceUnavailablePage failure={state.failure} onRetry={() => void runtime.session.retryStart()} />;
  }
  return <RouterProvider router={runtime.router} />;
}

export function AppRoot({ runtime }: { runtime: AppRuntime }) {
  return (
    <QueryClientProvider client={runtime.queryClient}>
      <SessionProvider controller={runtime.session}>
        <AppGate runtime={runtime} />
      </SessionProvider>
    </QueryClientProvider>
  );
}
