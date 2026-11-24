import { useLayoutEffect, useRef, useState } from "react";
import type { ArtistBioDTO, ArtistImageDTO } from "../api/decoders";
import styles from "./artist-bio.module.css";

const sourceNames: Record<ArtistImageDTO["source"] | ArtistBioDTO["source"], string> = {
  fanarttv: "Fanart.tv",
  theaudiodb: "TheAudioDB",
  commons: "Wikimedia Commons",
  wikipedia: "维基百科",
};

/** 外部给的地址只在是 http(s) 时做成链接，其余按纯文本显示。 */
function safeUrl(url: string | null): string | null {
  if (url === null) return null;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : null;
  } catch {
    return null;
  }
}

function ExternalLink({ href, children }: { href: string | null; children: string }) {
  const safe = safeUrl(href);
  if (safe === null) return <>{children}</>;
  return (
    <a href={safe} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  );
}

/** 署名里的一项：“简介：维基百科 · CC BY-SA 4.0”“图片：Wikimedia Commons · 作者 · CC BY 4.0”。 */
function Credit({ label, source, sourceUrl, author, license, licenseUrl }: { label: string; source: string; sourceUrl: string; author: string | null; license: string | null; licenseUrl: string | null }) {
  return (
    <span>
      {label}：<ExternalLink href={sourceUrl}>{source}</ExternalLink>
      {author === null ? null : <> · {author}</>}
      {license === null ? null : (
        <>
          {" · "}
          <ExternalLink href={licenseUrl}>{license}</ExternalLink>
        </>
      )}
    </span>
  );
}

/**
 * 艺人页头下方的简介与署名（M8）：简介默认最多 4 行，超出时可以展开；署名写出简介与图片各自的来源、作者与许可（外部链接在新窗口打开）。
 * 只有图片没有简介时只显示署名。两样都没有时不渲染。
 */
export function ArtistBio({ bio, image }: { bio: ArtistBioDTO | null; image: ArtistImageDTO | null }) {
  const textRef = useRef<HTMLParagraphElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const text = bio?.text ?? null;

  // 折叠时内容高于显示高度才给“展开”；宽度变化（转屏、拖窗口）后重新判断。
  useLayoutEffect(() => {
    const element = textRef.current;
    if (element === null || expanded) return;
    const measure = () => setOverflowing(element.scrollHeight > element.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [text, expanded]);

  if (bio === null && image === null) return null;
  return (
    <section className={styles.bio} aria-label="艺人简介">
      {bio === null ? null : (
        <>
          <p ref={textRef} className={styles.text} lang={bio.lang === "zh" ? "zh-CN" : "en"} data-expanded={expanded ? "" : undefined}>
            {bio.text}
          </p>
          {overflowing || expanded ? (
            <button type="button" className={styles.toggle} aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
              {expanded ? "收起" : "展开"}
            </button>
          ) : null}
        </>
      )}
      <p className={styles.credits}>
        {bio === null ? null : <Credit label="简介" source={sourceNames[bio.source]} sourceUrl={bio.source_url} author={null} license={bio.license} licenseUrl={bio.license_url} />}
        {image === null ? null : (
          <Credit label="图片" source={sourceNames[image.source]} sourceUrl={image.source_url} author={image.author} license={image.license} licenseUrl={image.license_url} />
        )}
      </p>
    </section>
  );
}
