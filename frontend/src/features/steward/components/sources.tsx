import { domainOf } from "../tool-labels";
import { safeHref } from "./answer";
import styles from "./thread.module.css";

type Source = { title: string; url: string };
type SourceRow = { key: string; href: string | null; text: string; domain: string | null };

/**
 * 外部来源的列表（C8）：标题（空时用域名）与灰色的域名。只有 http、https 的地址是链接，新标签页打开、不带来源页；
 * 其他地址只显示标题，标题也没有时不列。标题与地址来自外部网页，只按文本节点渲染。
 */
export function SourceList({ sources, label, className }: { sources: readonly Source[]; label: string; className?: string | undefined }) {
  const rows = sources.flatMap((source): SourceRow[] => {
    const href = safeHref(source.url);
    const domain = href === null ? null : domainOf(href);
    const title = source.title.trim();
    if (href === null || domain === null) return title === "" ? [] : [{ key: source.url, href: null, text: title, domain: null }];
    return [{ key: source.url, href, text: title === "" ? domain : title, domain: title === "" ? null : domain }];
  });
  if (rows.length === 0) return null;
  return (
    <ol className={className ?? styles.sources} aria-label={label}>
      {rows.map((row, index) => (
        <li key={`${index}:${row.key}`}>
          {row.href === null ? (
            <span className={styles.sourceTitle}>{row.text}</span>
          ) : (
            <a className={styles.sourceTitle} href={row.href} target="_blank" rel="noopener noreferrer">
              {row.text}
            </a>
          )}
          {row.domain === null ? null : <span className={styles.sourceDomain}>{row.domain}</span>}
        </li>
      ))}
    </ol>
  );
}

/** 回答下面的“来源”：这一回合回答引用的网页（模型自带搜索给出的引用，已按地址去重）。 */
export function Citations({ citations }: { citations: readonly Source[] }) {
  if (!citations.some((citation) => safeHref(citation.url) !== null || citation.title.trim() !== "")) return null;
  return (
    <div className={styles.citations}>
      <p className={styles.citationsTitle}>来源</p>
      <SourceList sources={citations} label="来源" />
    </div>
  );
}
