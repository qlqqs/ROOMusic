import { Link } from "@tanstack/react-router";
import { Panel, QueryContent, Skeleton, StatList, VisuallyHidden } from "../../../shared/ui";
import { formatCount } from "../../../shared/lib/number";
import { useLibraryTotals } from "../hooks/use-library-totals";
import styles from "./library-summary.module.css";

/** 概览的资料库区块：专辑数与需要关注的专辑数，两个数字分别链接到全部专辑与只看需要关注的专辑。 */
export function LibrarySummary({ isAdmin }: { isAdmin: boolean }) {
  const totals = useLibraryTotals();
  return (
    <Panel title="资料库">
      <QueryContent query={totals} loading={<Skeleton label="正在加载专辑数量" lines={2} />}>
        {(data) =>
          data.total === 0 ? (
            <p>
              {isAdmin
                ? "资料库里还没有专辑。添加音乐目录并扫描后就会出现。"
                : "资料库里还没有专辑。"}
            </p>
          ) : (
            <StatList
              items={[
                {
                  label: "专辑",
                  value: (
                    <Link to="/albums" className={styles.statLink}>
                      {formatCount(data.total)}
                      <VisuallyHidden>，查看全部专辑</VisuallyHidden>
                    </Link>
                  ),
                },
                {
                  label: "需要关注",
                  value: (
                    <Link to="/albums" search={{ attention: "required" }} className={styles.statLink}>
                      {formatCount(data.attention)}
                      <VisuallyHidden>，查看需要关注的专辑</VisuallyHidden>
                    </Link>
                  ),
                },
              ]}
            />
          )
        }
      </QueryContent>
    </Panel>
  );
}
