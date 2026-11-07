import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// 只认领第三方的脚本模块：第三方引入的 CSS 留在入口样式表里，样式的拆分与顺序保持原样。
function thirdPartyScript(pattern: RegExp) {
  return (moduleId: string) => pattern.test(moduleId) && !/\.css(\?|$)/.test(moduleId);
}

export default defineConfig({
  plugins: [react()],
  build: {
    // Go embeds this directory so the production binary serves the exact
    // frontend build from the same origin as the REST API.
    outDir: "../backend/cmd/roomusic/web",
    emptyOutDir: true,
    // 支持 2023 年以后的常青浏览器（ADR 0001 第 5 项）：color-mix() 等特性要求 Chrome/Edge 111、Firefox 113、Safari 16.4。
    target: ["chrome111", "edge111", "firefox113", "safari16.4"],
    rolldownOptions: {
      output: {
        // 第三方库各自成块：应用代码更新时这些块的内容哈希不变，浏览器继续用缓存；每个块也都低于 500 kB 的体积提示线。
        // 只按依赖拆分，不做路由懒加载，页面加载行为不变。优先级高的组先认领模块，其余第三方代码落进 vendor。
        codeSplitting: {
          groups: [
            { name: "react", test: thirdPartyScript(/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/), priority: 30 },
            { name: "tanstack", test: thirdPartyScript(/[\\/]node_modules[\\/]@tanstack[\\/]/), priority: 20 },
            { name: "lucide", test: thirdPartyScript(/[\\/]node_modules[\\/]lucide-react[\\/]/), priority: 20 },
            { name: "vendor", test: thirdPartyScript(/[\\/]node_modules[\\/]/), priority: 10 },
          ],
        },
      },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    unstubGlobals: true,
  },
});
