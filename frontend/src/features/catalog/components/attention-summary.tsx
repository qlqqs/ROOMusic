import { Link } from "@tanstack/react-router";
import { CircleAlert } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { PendingStatusRow, StatusRow } from "../../../shared/ui";
import { useLibraryTotals } from "../hooks/use-library-totals";

const icon = <CircleAlert size={16} />;

/**
 * 概览“资料库状态”里的需要关注一行（所有成员可见）：右侧是需要关注的专辑数，有的时候用强调色，整行进入只看需要关注的专辑列表；
 * 没有时写一句“没有需要关注的专辑”，不是链接。
 */
export function AttentionSummary() {
  const totals = useLibraryTotals();
  if (totals.data === undefined) return <PendingStatusRow icon={icon} label="需要关注" query={totals} />;
  const count = totals.data.attention;
  if (count === 0) return <StatusRow icon={icon} label="需要关注" detail="没有需要关注的专辑" value={formatCount(0)} />;
  return (
    <StatusRow
      icon={icon}
      label="需要关注"
      detail={`${formatCount(count)} 张专辑有待核对的问题`}
      value={formatCount(count)}
      tone="attention"
      renderLink={({ className, children }) => (
        <Link to="/albums" search={{ attention: "required" }} className={className}>
          {children}
        </Link>
      )}
    />
  );
}
