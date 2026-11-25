import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AppRoot } from "./app/providers";
import { createAppRuntime } from "./app/runtime";
import { reportRenderError } from "./features/system";
import "./styles/tokens.css";
import "./styles/base.css";

const container = document.getElementById("root");
if (container === null) throw new Error("缺少 #root 容器");

const runtime = createAppRuntime();
runtime.start();
// 渲染错误被错误边界接住后不会冒到 window，由这里交给页面报错上报；控制台照常输出。
const onRenderError = (error: unknown) => {
  console.error(error);
  reportRenderError(error);
};
createRoot(container, { onCaughtError: onRenderError, onUncaughtError: onRenderError }).render(
  <StrictMode>
    <AppRoot runtime={runtime} />
  </StrictMode>,
);
