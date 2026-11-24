import { useAbout } from "../hooks/use-insights";
import styles from "./source-link.module.css";

/** 登录页底部的一行：开源与源代码链接。接口读不到时不显示。 */
export function SourceLink() {
  const about = useAbout();
  if (about.data === undefined) return null;
  return (
    <p className={styles.line}>
      ROOMusic 是开源软件 ·{" "}
      <a href={about.data.source_url} target="_blank" rel="noreferrer">
        源代码
      </a>
    </p>
  );
}
