// steward 对话界面的公开入口：外壳的停靠面板与 /steward 路由按需加载这里（React.lazy），
// Markdown 渲染的依赖随它进入单独的代码块，不进首屏。
export { StewardPanel } from "./components/panel";
export { StewardPage } from "./components/steward-page";
