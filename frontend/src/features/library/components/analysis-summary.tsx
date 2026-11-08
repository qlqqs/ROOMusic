import { formatCount } from "../../../shared/lib/number";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useAnalysisProgress } from "../hooks/use-analysis-progress";
import styles from "./analysis-summary.module.css";

/**
 * 概览侧栏的“音频分析”（所有成员可见）：已分析 / 总数与细进度条；等扫描时说一句扫描结束后继续，全部分析过写“已完成”，
 * 有不能分析的曲目时写出首数。分析没有运行时只写“未运行”。
 */
export function AnalysisSummary() {
  const progress = useAnalysisProgress();
  return (
    <Panel title="音频分析">
      <QueryContent query={progress} loading={<Skeleton label="正在加载音频分析进度" lines={2} />}>
        {(data) => {
          if (data.state === "unavailable") return <p className={styles.note}>未运行</p>;
          const finished = data.analyzed + data.failed >= data.total;
          return (
            <div className={styles.body}>
              {finished ? (
                <p>已完成</p>
              ) : (
                <>
                  <p className={styles.count}>
                    {formatCount(data.analyzed)} / {formatCount(data.total)}
                  </p>
                  <progress className={styles.bar} aria-label="音频分析进度" max={data.total} value={data.analyzed} />
                </>
              )}
              {!finished && data.state === "waiting_for_scan" ? <p className={styles.note}>扫描结束后继续</p> : null}
              {data.failed > 0 ? <p className={styles.note}>{formatCount(data.failed)} 首无法分析</p> : null}
            </div>
          );
        }}
      </QueryContent>
    </Panel>
  );
}
