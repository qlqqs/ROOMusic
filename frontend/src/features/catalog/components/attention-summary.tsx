import { Link } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useLibraryTotals } from "../hooks/use-library-totals";
import styles from "./attention-summary.module.css";

/** 概览侧栏的“需要关注”：需要关注的专辑数，有的时候链接到只看需要关注的专辑列表。所有成员可见。 */
export function AttentionSummary() {
  const totals = useLibraryTotals();
  return (
    <Panel title="需要关注">
      <QueryContent query={totals} loading={<Skeleton label="正在加载需要关注的专辑数" lines={1} />}>
        {(data) => (
          <p className={styles.value}>
            {data.attention === 0 ? (
              formatCount(0)
            ) : (
              <Link to="/albums" search={{ attention: "required" }} className={styles.link} aria-label={`${formatCount(data.attention)} 张专辑需要关注`}>
                {formatCount(data.attention)}
              </Link>
            )}
          </p>
        )}
      </QueryContent>
    </Panel>
  );
}
