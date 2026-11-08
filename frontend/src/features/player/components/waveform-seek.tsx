import { useEffect, useRef } from "react";
import { useTrackAnalysis } from "../hooks/use-track-analysis";
import { usePlayerProgressSelector, usePlayerSelector } from "../player-provider";
import { SeekControl } from "./player-controls";

const BAR = 2;
const GAP = 1;

/**
 * 画波形：宽 W 像素画 floor(W/3) 根柱，每根取对应区间的最大峰值。按本曲最大峰值归一化（最响的一根占满高度，安静的曲目也看得出起伏），
 * 再按 ^0.6 拉伸（安静段也看得见），最低 1px；全静音时都是最低高度。已播放与未播放两色。
 */
function draw(canvas: HTMLCanvasElement, peaks: Uint8Array, ratio: number): void {
  const width = canvas.clientWidth;
  const height = canvas.clientHeight;
  const context = canvas.getContext("2d");
  if (context === null || width === 0 || height === 0) return;
  const scale = window.devicePixelRatio || 1;
  if (canvas.width !== Math.round(width * scale) || canvas.height !== Math.round(height * scale)) {
    canvas.width = Math.round(width * scale);
    canvas.height = Math.round(height * scale);
  }
  context.setTransform(scale, 0, 0, scale, 0, 0);
  context.clearRect(0, 0, width, height);
  const style = getComputedStyle(canvas);
  const played = style.getPropertyValue("--wave-played").trim();
  const rest = style.getPropertyValue("--wave-rest").trim();
  const loudest = peaks.reduce((max, peak) => Math.max(max, peak), 0);
  const bars = Math.floor(width / (BAR + GAP));
  for (let index = 0; index < bars; index += 1) {
    const from = Math.floor((index * peaks.length) / bars);
    const to = Math.max(from + 1, Math.floor(((index + 1) * peaks.length) / bars));
    let peak = 0;
    for (let at = from; at < to && at < peaks.length; at += 1) peak = Math.max(peak, peaks[at] ?? 0);
    const barHeight = loudest === 0 ? 1 : Math.max(1, (peak / loudest) ** 0.6 * height);
    context.fillStyle = (index + 0.5) / bars <= ratio ? played : rest;
    context.fillRect(index * (BAR + GAP), (height - barHeight) / 2, BAR, barHeight);
  }
}

function WaveCanvas({ peaks }: { peaks: Uint8Array }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  // 只订阅播放比例（约每秒 4 次），重画一张画布。
  const ratio = usePlayerProgressSelector((progress) => (progress.durationSeconds === null || progress.durationSeconds <= 0 ? 0 : progress.positionSeconds / progress.durationSeconds));
  const latest = useRef({ peaks, ratio });
  useEffect(() => {
    latest.current = { peaks, ratio };
    if (canvas.current !== null) draw(canvas.current, peaks, ratio);
  }, [peaks, ratio]);
  useEffect(() => {
    const element = canvas.current;
    if (element === null || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => draw(element, latest.current.peaks, latest.current.ratio));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return <canvas ref={canvas} aria-hidden="true" />;
}

/** 播放进度：当前曲目已分析时显示波形，否则（还没分析、分析失败、读取失败或加载中）是普通进度条。 */
export function WaveformSeek({ layout = "inline" }: { layout?: "inline" | "ends" }) {
  const trackId = usePlayerSelector((snapshot) => snapshot.current?.track.trackId ?? null);
  const analysis = useTrackAnalysis(trackId);
  return <SeekControl layout={layout} wave={analysis === null ? undefined : <WaveCanvas peaks={analysis.peaks} />} />;
}
