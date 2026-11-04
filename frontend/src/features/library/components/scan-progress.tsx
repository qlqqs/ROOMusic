import type { ScanDTO } from "../api/decoders";
import { cx } from "../../../shared/lib/class-names";
import { VisuallyHidden } from "../../../shared/ui";
import { progressFilesText, progressPercent, progressRootFilesText, progressStageText, progressValueText } from "../scan-progress";
import styles from "./scan-progress.module.css";

type ScanProgressProps = {
  scan: ScanDTO;
  /** 显示当前目录的名称与局部计数（管理员的扫描详情）；名称只在管理员的响应里才有。 */
  showRoot?: boolean;
};

/**
 * 扫描进度：只读的进度条（不是滑块，不可调节、不抢焦点）加当前阶段与累计文件数，概览与扫描详情共用。
 * 数值全部来自服务端：整体进度按各目录文件数加权、不会倒退；没有成功结束的扫描停在停下的位置，不补满、不动画。
 * 没有进度数据时进度条不给数值：运行中说“正在准备”，已结束的旧扫描不显示这一块。
 */
export function ScanProgress({ scan, showRoot = false }: ScanProgressProps) {
  const running = scan.status === "running";
  const percent = progressPercent(scan);
  if (percent === null && !running) return null;

  const stage = progressStageText(scan);
  const files = progressFilesText(scan);
  const stopped = !running && scan.status !== "succeeded";
  const rootName = showRoot ? (scan.root_progress?.name ?? null) : null;
  const rootFiles = showRoot && running ? progressRootFilesText(scan) : null;

  return (
    <div className={cx(styles.progress, stopped && styles.stopped)}>
      <div
        role="progressbar"
        aria-label="扫描进度"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent ?? undefined}
        aria-valuetext={progressValueText(scan)}
        className={styles.track}
      >
        {percent === null ? null : <div className={styles.fill} style={{ width: `${percent}%` }} />}
      </div>
      <p className={styles.summary} aria-hidden="true">
        {percent === null ? (
          <span>正在准备…</span>
        ) : (
          <>
            <span className={styles.percent}>{percent}%</span>
            {stage === null ? null : <span>{stopped ? `停在${stage}` : stage}</span>}
          </>
        )}
      </p>
      {files === null ? null : <p className={styles.meta}>{files}</p>}
      {rootName === null ? null : (
        <p className={styles.meta}>
          当前目录：{rootName}
          {rootFiles === null ? null : `（${rootFiles}）`}
        </p>
      )}
      {/* 礼貌型播报：只在阶段或目录变化时读一次（文字不变就不播报），不读每次轮询的数字。用 aria-live 而不是 role="status"：
          扫描详情的状态标签已经是这一区块的 status，终态由它播报。 */}
      {running ? (
        <VisuallyHidden>
          <span aria-live="polite">{stage ?? ""}</span>
        </VisuallyHidden>
      ) : null}
    </div>
  );
}
