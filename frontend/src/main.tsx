import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";

function App() {
  return (
    <main className="shell">
      <p className="eyebrow">本地音乐库</p>
      <h1>ROOMusic</h1>
      <p>首个可浏览纵向切片工程已就绪，下一步将接入管理员初始化和扫描流程。</p>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
