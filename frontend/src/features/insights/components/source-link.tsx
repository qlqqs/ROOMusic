import { useEffect, useState } from "react";
import { fetchAbout } from "../api/endpoints";
import styles from "./source-link.module.css";

/**
 * 登录页底部的一行：开源与源代码链接。登录前没有用户作用域，退出时查询缓存要清空，所以直接请求、不进查询缓存；读不到时不显示。
 */
export function SourceLink() {
  const [sourceUrl, setSourceUrl] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetchAbout(controller.signal).then((response) => setSourceUrl(response.data.source_url), () => undefined);
    return () => controller.abort();
  }, []);
  if (sourceUrl === null) return null;
  return (
    <p className={styles.line}>
      ROOMusic 是开源软件 ·{" "}
      <a href={sourceUrl} target="_blank" rel="noreferrer">
        源代码
      </a>
    </p>
  );
}
