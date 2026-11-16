import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "./answer.module.css";

/** 只有 http、https 的地址渲染成链接；其他协议（javascript:、data: 等）与相对地址只显示文字。 */
export function safeHref(href: string | undefined): string | null {
  if (href === undefined) return null;
  try {
    const url = new URL(href);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
  } catch {
    return null;
  }
}

// 链接在新标签页打开；图片不加载，显示替代文字；表格一律左对齐（不用 Markdown 里的对齐标记），外面套一层可横向滚动的容器。
const components: Components = {
  a: ({ href, children }) => {
    const target = safeHref(href);
    return target === null ? <span>{children}</span> : (
      <a href={target} target="_blank" rel="noreferrer noopener">
        {children}
      </a>
    );
  },
  img: ({ alt }) => <span className={styles.image}>{alt === undefined || alt === "" ? "［图片］" : `［图片：${alt}］`}</span>,
  table: ({ children }) => (
    <div className={styles.tableWrap}>
      <table>{children}</table>
    </div>
  ),
  th: ({ children }) => <th>{children}</th>,
  td: ({ children }) => <td>{children}</td>,
};

/**
 * 回答的 Markdown（GFM：表格、列表、删除线、任务列表）。不启用原始 HTML：回答里的 HTML 标签按文字显示；
 * 流式过程中每次增量整段重新解析。cursor 为真时在末尾显示输入光标。
 */
export function Answer({ text, cursor = false }: { text: string; cursor?: boolean }) {
  return (
    <div className={styles.answer} data-cursor={cursor ? "" : undefined}>
      <Markdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </Markdown>
    </div>
  );
}
