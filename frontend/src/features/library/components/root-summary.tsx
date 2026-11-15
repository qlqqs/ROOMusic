import { Link } from "@tanstack/react-router";
import { FolderOpen } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { PendingStatusRow, StatusRow } from "../../../shared/ui";
import { summarizeRoots, useRoots } from "../hooks/use-roots";

const icon = <FolderOpen size={16} />;

/** 概览“资料库状态”里的音乐目录一行（管理员）：右侧是启用的目录数，有停用的另写一句；整行进入扫描目录。 */
export function RootSummary() {
  const roots = useRoots();
  if (roots.data === undefined) return <PendingStatusRow icon={icon} label="音乐目录" query={roots} />;
  const summary = summarizeRoots(roots.data);
  const detail = roots.data.length === 0 ? "还没有音乐目录" : summary.disabled > 0 ? `${formatCount(summary.disabled)} 个已停用` : undefined;
  return (
    <StatusRow
      icon={icon}
      label="音乐目录"
      detail={detail}
      value={formatCount(summary.active)}
      renderLink={({ className, children }) => (
        <Link to="/settings/library" className={className}>
          {children}
        </Link>
      )}
    />
  );
}
