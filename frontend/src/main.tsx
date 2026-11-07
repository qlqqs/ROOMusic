import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AppRoot } from "./app/providers";
import { createAppRuntime } from "./app/runtime";
import "./styles/tokens.css";
import "./styles/base.css";

const container = document.getElementById("root");
if (container === null) throw new Error("缺少 #root 容器");

const runtime = createAppRuntime();
runtime.start();
createRoot(container).render(
  <StrictMode>
    <AppRoot runtime={runtime} />
  </StrictMode>,
);
