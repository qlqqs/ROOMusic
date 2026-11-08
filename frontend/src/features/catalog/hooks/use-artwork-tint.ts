import { useEffect, useState } from "react";
import { artworkPath } from "../api/endpoints";
import type { ArtworkDTO } from "../api/decoders";

const SIZE = 24;

/** 按封面资源记住算出的淡色（算不出来是 null），同一张封面只算一次。 */
const tints = new Map<string, string | null>();

/** RGB（0–255）转 HSL：色相 0–360，饱和度与亮度 0–1。 */
function hslOf(red: number, green: number, blue: number): [number, number, number] {
  const r = red / 255;
  const g = green / 255;
  const b = blue / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const lightness = (max + min) / 2;
  const delta = max - min;
  if (delta === 0) return [0, 0, lightness];
  const saturation = delta / (1 - Math.abs(2 * lightness - 1));
  const hue = max === r ? ((g - b) / delta + 6) % 6 : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4;
  return [hue * 60, saturation, lightness];
}

/**
 * 缩到 24×24 后，跳过透明、近白与近黑的像素，按饱和度加权求平均色；结果固定成浅色 hsl(H, ≤45%, 90%)，
 * 上面的正文与次要文字对比度不下降。灰度封面（平均色饱和度不到 10%）没有可取的颜色，返回 null。
 */
function tintOf(image: HTMLImageElement): string | null {
  const canvas = document.createElement("canvas");
  canvas.width = SIZE;
  canvas.height = SIZE;
  const context = canvas.getContext("2d", { willReadFrequently: true });
  if (context === null) return null;
  context.drawImage(image, 0, 0, SIZE, SIZE);
  const { data } = context.getImageData(0, 0, SIZE, SIZE);
  let red = 0;
  let green = 0;
  let blue = 0;
  let weight = 0;
  for (let index = 0; index < data.length; index += 4) {
    const [r = 0, g = 0, b = 0, alpha = 0] = data.subarray(index, index + 4);
    if (alpha < 128) continue;
    const [, saturation, lightness] = hslOf(r, g, b);
    if (lightness > 0.92 || lightness < 0.08) continue;
    red += r * saturation;
    green += g * saturation;
    blue += b * saturation;
    weight += saturation;
  }
  if (weight === 0) return null;
  const [hue, saturation] = hslOf(red / weight, green / weight, blue / weight);
  if (saturation < 0.1) return null;
  return `hsl(${Math.round(hue)}, ${Math.round(Math.min(saturation, 0.45) * 100)}%, 90%)`;
}

/**
 * 封面的主色对应的淡色，用作页头渐变的底色。封面与 Cover 同一地址（同源，命中浏览器缓存）；
 * 没有封面、加载失败或算不出颜色时是 null（调用方用中性淡色），不重试。
 */
export function useArtworkTint(artwork: ArtworkDTO | null): string | null {
  const id = artwork?.resource_id ?? null;
  const [loaded, setLoaded] = useState<{ id: string; tint: string | null } | null>(null);
  useEffect(() => {
    if (id === null || tints.has(id)) return;
    let active = true;
    const image = new Image();
    const settle = (tint: string | null) => {
      tints.set(id, tint);
      if (active) setLoaded({ id, tint });
    };
    image.onload = () => settle(tintOf(image));
    image.onerror = () => settle(null);
    image.src = artworkPath(id);
    return () => {
      active = false;
    };
  }, [id]);
  if (id === null) return null;
  if (tints.has(id)) return tints.get(id) ?? null;
  return loaded?.id === id ? loaded.tint : null;
}
