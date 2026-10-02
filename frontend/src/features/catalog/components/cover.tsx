import { useState } from "react";
import { Button } from "../../../shared/ui";
import type { ArtworkDTO } from "../api/decoders";
import { artworkPath } from "../api/endpoints";
import styles from "./cover.module.css";

type CoverProps = {
  /** 接口给的封面摘要；没有封面时为 null。 */
  artwork: ArtworkDTO | null;
  title: string;
  /** card：列表卡片里的装饰（标题已在链接名称里）；detail：详情页，封面有替代文字，失败后可以重新加载。 */
  variant: "card" | "detail";
};

/**
 * 带裱边的正方形封面，四种状态各自可见：加载中（同尺寸的静态底色）、已加载、没有封面（深色底上排标题）、
 * 加载失败（同样的版面加“封面无法加载”）。不加滤镜、不裁切真实封面，只引用同源的封面接口，不拉外部图片。
 */
export function Cover({ artwork, title, variant }: CoverProps) {
  const [attempt, setAttempt] = useState(0);
  if (artwork === null) {
    return (
      <div className={styles.cover} data-variant={variant} aria-hidden={variant === "card" ? true : undefined}>
        <div className={styles.frame} data-state="none" role={variant === "detail" ? "img" : undefined} aria-label={variant === "detail" ? `《${title}》没有封面` : undefined}>
          <TextCover title={title} />
        </div>
      </div>
    );
  }
  // 换了封面或点了“重新加载封面”，就用新的 key 重新挂载图片：状态回到“加载中”，浏览器重新发请求。
  return <CoverImage key={`${artwork.resource_id}#${attempt}`} artwork={artwork} title={title} variant={variant} onRetry={() => setAttempt((count) => count + 1)} />;
}

function CoverImage({ artwork, title, variant, onRetry }: CoverProps & { artwork: ArtworkDTO; onRetry: () => void }) {
  const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");
  return (
    <div className={styles.cover} data-variant={variant} aria-hidden={variant === "card" ? true : undefined}>
      <div className={styles.frame} data-state={state}>
        {state === "failed" ? (
          <TextCover title={title} caption="封面无法加载" />
        ) : (
          <img
            className={styles.image}
            src={artworkPath(artwork.resource_id)}
            width={artwork.width}
            height={artwork.height}
            alt={variant === "card" ? "" : `《${title}》封面`}
            loading={variant === "card" ? "lazy" : "eager"}
            decoding="async"
            onLoad={() => setState("loaded")}
            onError={() => setState("failed")}
          />
        )}
      </div>
      {state === "failed" && variant === "detail" ? <Button onClick={onRetry}>重新加载封面</Button> : null}
    </div>
  );
}

// 标题在封面里只是版面：页面上已经有标题（卡片的链接名称、详情页的 h1），读屏不再重复。
function TextCover({ title, caption }: { title: string; caption?: string }) {
  return (
    <span className={styles.text}>
      <span className={styles.textTitle} aria-hidden="true">
        {title}
      </span>
      {caption === undefined ? null : <span className={styles.caption}>{caption}</span>}
    </span>
  );
}
