import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// 只认领第三方的脚本模块：第三方引入的 CSS 留在入口样式表里，样式的拆分与顺序保持原样。
function thirdPartyScript(pattern: RegExp) {
  return (moduleId: string) => pattern.test(moduleId) && !/\.css(\?|$)/.test(moduleId);
}

// 回答的 Markdown 渲染（react-markdown、remark-gfm 及其间接依赖）：只有管家的对话界面用到，单独成块，随对话界面按需加载，不进首屏的 vendor。
const markdownPackages =
  /[\\/]node_modules[\\/](react-markdown|remark-[^\\/]+|mdast-util-[^\\/]+|micromark[^\\/]*|hast-util-[^\\/]+|unist-util-[^\\/]+|unified|vfile[^\\/]*|bail|ccount|character-entities[^\\/]*|character-reference-invalid|comma-separated-tokens|space-separated-tokens|decode-named-character-reference|devlop|estree-util-is-identifier-name|extend|html-url-attributes|inline-style-parser|style-to-object|style-to-js|is-alphabetical|is-alphanumerical|is-decimal|is-hexadecimal|is-plain-obj|longest-streak|markdown-table|escape-string-regexp|parse-entities|stringify-entities|property-information|trim-lines|trough|zwitch|@ungap[\\/]structured-clone|debug|ms)[\\/]/;

export default defineConfig({
  plugins: [react()],
  build: {
    // Go 内嵌 web 目录，生产二进制与 REST API 同源提供这份构建；dist 不入库，生产镜像在 Node 阶段生成。
    outDir: "../backend/cmd/roomusic/web/dist",
    emptyOutDir: true,
    // 支持 2023 年以后的常青浏览器（ADR 0001 第 5 项）：color-mix() 等特性要求 Chrome/Edge 111、Firefox 113、Safari 16.4。
    target: ["chrome111", "edge111", "firefox113", "safari16.4"],
    rolldownOptions: {
      output: {
        // 第三方库各自成块：应用代码更新时这些块的内容哈希不变，浏览器继续用缓存；每个块也都低于 500 kB 的体积提示线。
        // 只按依赖拆分，页面加载行为不变。优先级高的组先认领模块，其余第三方代码落进 vendor。
        codeSplitting: {
          groups: [
            { name: "react", test: thirdPartyScript(/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/), priority: 30 },
            { name: "tanstack", test: thirdPartyScript(/[\\/]node_modules[\\/]@tanstack[\\/]/), priority: 20 },
            { name: "lucide", test: thirdPartyScript(/[\\/]node_modules[\\/]lucide-react[\\/]/), priority: 20 },
            { name: "markdown", test: thirdPartyScript(markdownPackages), priority: 20 },
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
