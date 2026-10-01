import { formatCount } from "../../../shared/lib/number";
import { Panel, QueryContent, Skeleton, StatList } from "../../../shared/ui";
import { summarizeMembers, useMembers } from "../hooks/use-members";

/** 概览的成员区块（管理员）：启用与已停用的成员数。 */
export function MemberSummary() {
  const members = useMembers();
  return (
    <Panel title="成员">
      <QueryContent query={members} loading={<Skeleton label="正在加载成员" lines={2} />}>
        {(items) => {
          const summary = summarizeMembers(items);
          return (
            <StatList
              items={[
                { label: "启用", value: formatCount(summary.enabled) },
                { label: "已停用", value: formatCount(summary.disabled) },
              ]}
            />
          );
        }}
      </QueryContent>
    </Panel>
  );
}
