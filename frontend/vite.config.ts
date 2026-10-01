import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  build: {
    // Go embeds this directory so the production binary serves the exact
    // frontend build from the same origin as the REST API.
    outDir: "../backend/cmd/roomusic/web",
    emptyOutDir: true,
    // 支持 2023 年以后的常青浏览器（ADR 0001 第 5 项）：color-mix() 等特性要求 Chrome/Edge 111、Firefox 113、Safari 16.4。
    target: ["chrome111", "edge111", "firefox113", "safari16.4"],
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    unstubGlobals: true,
  },
});
