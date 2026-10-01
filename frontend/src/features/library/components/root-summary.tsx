import { Link } from "@tanstack/react-router";
import { formatCount } from "../../../shared/lib/number";
import { Panel, QueryContent, Skeleton, StatList } from "../../../shared/ui";
import { summarizeRoots, useRoots } from "../hooks/use-roots";

/** 概览的目录区块（管理员）：启用与已停用的目录数，并链接到扫描目录页。 */
export function RootSummary() {
  const roots = useRoots();
  return (
    <Panel title="音乐目录" action={<Link to="/library">管理目录</Link>}>
      <QueryContent query={roots} loading={<Skeleton label="正在加载目录" lines={2} />}>
        {(items) => {
          if (items.length === 0) return <p>还没有登记音乐目录。</p>;
          const summary = summarizeRoots(items);
          return (
            <StatList
              items={[
                { label: "启用", value: formatCount(summary.active) },
                { label: "已停用", value: formatCount(summary.disabled) },
              ]}
            />
          );
        }}
      </QueryContent>
    </Panel>
  );
}
