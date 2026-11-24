import { Disc3, ListMusic } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import type { ArtworkDTO } from "../api/decoders";
import { artworkPath } from "../api/endpoints";
import styles from "./cover.module.css";

type CoverProps = {
  /** 接口给的封面摘要；没有封面时为 null。 */
  artwork: ArtworkDTO | null;
  title: string;
  /**
   * card：列表卡片里的装饰（标题已在链接名称里）；detail：详情页，封面有替代文字，失败后可以重新加载；
   * thumb：侧栏这类小尺寸的装饰缩略图，没有裱边，没有封面或加载失败时只显示歌单图标，不排标题、也不给重试。
   */
  variant: "card" | "detail" | "thumb";
};

/**
 * 带裱边的正方形封面，四种状态各自可见：加载中（同尺寸的静态底色）、已加载、没有封面（浅底上一枚唱片图标）、
 * 加载失败（同样的占位加“封面无法加载”）。占位里不再排标题：标题就在旁边（卡片标题、详情页的 h1），排两遍只是重复。
 * 不加滤镜、不裁切真实封面，只引用同源的封面接口，不拉外部图片。
 */
export function Cover({ artwork, title, variant }: CoverProps) {
  const [attempt, setAttempt] = useState(0);
  if (variant === "thumb") return <ThumbCover key={artwork?.resource_id ?? "none"} artwork={artwork} />;
  if (artwork === null) {
    return (
      <div className={styles.cover} data-variant={variant} aria-hidden={variant === "card" ? true : undefined}>
        <div className={styles.frame} data-state="none" role={variant === "detail" ? "img" : undefined} aria-label={variant === "detail" ? `《${title}》没有封面` : undefined}>
          <Placeholder />
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
          <Placeholder caption="封面无法加载" />
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

/** 小缩略图：同尺寸底色上懒加载图片；没有封面或加载失败时是图标。整个是装饰，名称在旁边的链接里。 */
function ThumbCover({ artwork }: { artwork: ArtworkDTO | null }) {
  const [failed, setFailed] = useState(false);
  const showImage = artwork !== null && !failed;
  return (
    <span className={styles.thumb} data-state={showImage ? "image" : "none"} aria-hidden="true">
      {showImage ? (
        <img
          className={styles.thumbImage}
          src={artworkPath(artwork.resource_id)}
          width={artwork.width}
          height={artwork.height}
          alt=""
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
      ) : (
        <ListMusic size={14} />
      )}
    </span>
  );
}

// 图标只是装饰：卡片里封面整体对读屏隐藏，详情页由外层的 role="img" 给出“《专辑》没有封面”。
function Placeholder({ caption }: { caption?: string }) {
  return (
    <span className={styles.placeholder}>
      <Disc3 className={styles.placeholderIcon} strokeWidth={1.25} aria-hidden="true" />
      {caption === undefined ? null : <span className={styles.caption}>{caption}</span>}
    </span>
  );
}
