import type { PeqCurve as Curve } from "./api";
import styles from "./peq.module.css";

const WIDTH = 600;
const HEIGHT = 180;
const MIN_HZ = 20;
const MAX_HZ = 20000;
const freqTicks = [{ hz: 100, label: "100" }, { hz: 1000, label: "1k" }, { hz: 10000, label: "10k" }];
const colors = ["var(--color-accent-700)", "var(--color-signal-enhanced)", "var(--color-signal-high)", "var(--color-neutral-700)"];

function x(hz: number): number {
  return ((Math.log10(hz) - Math.log10(MIN_HZ)) / (Math.log10(MAX_HZ) - Math.log10(MIN_HZ))) * WIDTH;
}

function channelsName(channels: Curve["channels"]): string {
  if (channels === "all") return "全部声道";
  return channels.map((channel) => (channel === 0 ? "左" : channel === 1 ? "右" : `声道 ${channel + 1}`)).join("、");
}

/** 频响曲线：横轴 20 Hz–20 kHz 对数，纵轴按曲线最大偏离取 ±12 dB 起、6 dB 一档。 */
export function PeqCurve({ curves }: { curves: readonly Curve[] }) {
  const peak = Math.max(12, ...curves.flatMap((curve) => curve.points.map(([, db]) => Math.abs(db))));
  const range = Math.ceil(peak / 6) * 6;
  const y = (db: number) => HEIGHT / 2 - (db / range) * (HEIGHT / 2);
  const labelled = curves.length > 1;
  return (
    <figure className={styles.curve}>
      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="img" aria-label="频响曲线" preserveAspectRatio="none">
        {freqTicks.map((tick) => <line key={tick.hz} x1={x(tick.hz)} x2={x(tick.hz)} y1={0} y2={HEIGHT} className={styles.grid} />)}
        {[-range / 2, range / 2].map((db) => <line key={db} x1={0} x2={WIDTH} y1={y(db)} y2={y(db)} className={styles.grid} />)}
        <line x1={0} x2={WIDTH} y1={y(0)} y2={y(0)} className={styles.zero} />
        {curves.map((curve, index) => (
          <polyline key={index} points={curve.points.map(([hz, db]) => `${x(hz).toFixed(1)},${y(db).toFixed(1)}`).join(" ")} fill="none" stroke={colors[index % colors.length]} strokeWidth={2} vectorEffect="non-scaling-stroke" />
        ))}
      </svg>
      <div className={styles.curveAxis} aria-hidden="true">
        {freqTicks.map((tick) => <span key={tick.hz} style={{ left: `${(x(tick.hz) / WIDTH) * 100}%` }}>{tick.label}</span>)}
      </div>
      <figcaption className={styles.curveLegend}>
        {labelled ? curves.map((curve, index) => (
          <span key={index}><i style={{ background: colors[index % colors.length] }} />{channelsName(curve.channels)}</span>
        )) : null}
        <span className={styles.curveRange}>±{range} dB</span>
      </figcaption>
    </figure>
  );
}
