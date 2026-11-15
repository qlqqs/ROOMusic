import { Link } from "@tanstack/react-router";
import { Users } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { PendingStatusRow, StatusRow } from "../../../shared/ui";
import { summarizeMembers, useMembers } from "../hooks/use-members";

const icon = <Users size={16} />;

/** 概览“资料库状态”里的成员一行（管理员）：右侧是启用的成员数，有停用的另写一句；整行进入成员管理。 */
export function MemberSummary() {
  const members = useMembers();
  if (members.data === undefined) return <PendingStatusRow icon={icon} label="成员" query={members} />;
  const summary = summarizeMembers(members.data);
  return (
    <StatusRow
      icon={icon}
      label="成员"
      detail={summary.disabled > 0 ? `${formatCount(summary.disabled)} 位已停用` : undefined}
      value={formatCount(summary.enabled)}
      renderLink={({ className, children }) => (
        <Link to="/settings/members" className={className}>
          {children}
        </Link>
      )}
    />
  );
}
