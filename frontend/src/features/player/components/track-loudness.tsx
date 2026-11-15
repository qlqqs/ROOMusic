import { useTrackAnalysis } from "../hooks/use-track-analysis";

/** 响度数值：一位小数，负号用 −，0 写 0.0（与信号路径的测量格相同）。 */
function formatLevel(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  if (rounded === 0) return "0.0";
  return `${rounded < 0 ? "−" : ""}${Math.abs(rounded).toFixed(1)}`;
}

/** 一首曲目的整体响度，渲染成描述列表的一项（“响度 −14.2 LUFS”）；还没有分析结果时什么也不渲染。专辑页的曲目信息经插槽使用。 */
export function TrackLoudness({ trackId }: { trackId: string }) {
  const lufs = useTrackAnalysis(trackId)?.integratedLufs ?? null;
  if (lufs === null) return null;
  return <>
    <dt>响度</dt>
    <dd>{formatLevel(lufs)} LUFS</dd>
  </>;
}
