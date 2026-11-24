import { useState, type CSSProperties } from "react";
import { artworkPath } from "../../catalog";
import styles from "./artist-monogram.module.css";

const segmenter = typeof Intl.Segmenter === "function" ? new Intl.Segmenter(undefined, { granularity: "grapheme" }) : null;

function firstGrapheme(text: string): string {
  return segmenter?.segment(text)[Symbol.iterator]().next().value?.segment ?? Array.from(text)[0] ?? "";
}

/**
 * 圆章里的字（没有艺人图片或图片读不出来时显示）：名字是几个拉丁字母开头的词时取第一个与最后一个词的首字母（“Leonard Bernstein” → “LB”），
 * 其余（中文、日文、单个词）取第一个字；拉丁字母转大写。
 */
export function monogramOf(name: string): string {
  const words = name.trim().split(/\s+/).filter((word) => word !== "");
  const first = words[0];
  const last = words.at(-1);
  if (first === undefined || last === undefined) return "";
  const latin = /^\p{Script=Latin}/u;
  if (words.length > 1 && latin.test(first) && latin.test(last)) return `${firstGrapheme(first)}${firstGrapheme(last)}`.toLocaleUpperCase();
  return firstGrapheme(first).toLocaleUpperCase();
}

/** 由艺人 ID 得到一个稳定的色相：同一位艺人每次颜色相同，相邻的艺人颜色不同。 */
function hueOf(id: string): number {
  let hash = 0;
  for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) % 360;
  return hash;
}

/**
 * 艺人的圆章：有自动整理补上的艺人图片时显示图片（圆形裁切），没有或读不出来时是名字缩写放在按 ID 取色的淡色圆里。
 * 装饰性的，名字总在旁边写出来。size 是列表里的小章或艺人页头的大章。
 */
export function ArtistMonogram({ id, name, image = null, size = "small" }: { id: string; name: string; image?: string | null; size?: "small" | "large" }) {
  // 读失败的那张图记下来：换了一张图（重新整理、撤销后补了别的）时再试。
  const [failed, setFailed] = useState<string | null>(null);
  const style = { "--monogram-hue": hueOf(id) } as CSSProperties;
  if (image !== null && failed !== image) {
    return (
      <span className={styles.monogram} data-size={size} data-image="" style={style} aria-hidden="true">
        <img className={styles.image} src={artworkPath(image)} alt="" loading="lazy" decoding="async" onError={() => setFailed(image)} />
      </span>
    );
  }
  return (
    <span className={styles.monogram} data-size={size} style={style} aria-hidden="true">
      {monogramOf(name)}
    </span>
  );
}
