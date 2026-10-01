import { createMemoryHistory } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { afterEach } from "vitest";
import { AppRoot } from "../app/providers";
import { createAppRuntime, type AppRuntime, type AppRuntimeOptions } from "../app/runtime";

const runtimes: AppRuntime[] = [];

afterEach(() => {
  for (const runtime of runtimes.splice(0)) runtime.dispose();
});

/** 用内存历史渲染完整应用并开始确认会话；默认不开跨标签页通道。测试结束时释放运行时。 */
export function renderApp(options: { path?: string } & Omit<AppRuntimeOptions, "history"> = {}) {
  const { path = "/", ...runtimeOptions } = options;
  const history = createMemoryHistory({ initialEntries: [path] });
  const runtime = createAppRuntime({ history, channel: null, ...runtimeOptions });
  runtimes.push(runtime);
  runtime.start();
  const view = render(<AppRoot runtime={runtime} />);
  return { runtime, history, view };
}
