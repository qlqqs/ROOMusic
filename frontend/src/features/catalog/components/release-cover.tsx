import { useState } from "react";
import type { ReleaseArtworkDTO } from "../../../api";
import { artworkURL, coverFallbackLabel } from "../model/display";

export type CoverStatus = "absent" | "loading" | "ready" | "broken";

type ReleaseCoverProps = {
  artwork: ReleaseArtworkDTO | null;
  title: string;
  className?: string;
  // 卡片本身是 button 时不能嵌套重试按钮；仅在非嵌套上下文（如详情抽屉）开启。
  allowRetry?: boolean;
};

// 封面只由受控 resource_id 拼接同源受鉴权 URL；加载失败只影响当前组件，
// 可通过重试清除 broken 状态。
export function ReleaseCover({ artwork, title, className, allowRetry = false }: ReleaseCoverProps) {
  const url = artworkURL(artwork);
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const [loadedKey, setLoadedKey] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const attemptKey = url === null ? null : `${url}#${attempt}`;
  const status: CoverStatus = url === null
    ? "absent"
    : failedKey === attemptKey
      ? "broken"
      : loadedKey === attemptKey
        ? "ready"
        : "loading";
  const classes = ["release-cover", className ?? "", `cover-${status}`].join(" ").trim();
  return (
    <div className={classes} data-cover-status={status}>
      {url !== null && status !== "broken" && (
        <img
          key={attemptKey}
          src={url}
          alt={`${title} 封面`}
          loading="lazy"
          onLoad={() => setLoadedKey(attemptKey)}
          onError={() => setFailedKey(attemptKey)}
        />
      )}
      {status !== "ready" && (
        <span className="cover-fallback" aria-hidden={status === "loading"}>
          {coverFallbackLabel(title)}
        </span>
      )}
      {status === "broken" && allowRetry && (
        <button
          type="button"
          className="cover-retry"
          aria-label={`重新加载 ${title} 封面`}
          onClick={(event) => {
            event.stopPropagation();
            setAttempt((value) => value + 1);
          }}
        >
          重试
        </button>
      )}
    </div>
  );
}
