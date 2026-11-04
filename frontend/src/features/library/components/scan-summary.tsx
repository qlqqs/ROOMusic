import { Link } from "@tanstack/react-router";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useActiveScan } from "../hooks/use-active-scan";
import { ActiveScan } from "./active-scan";

/**
 * 概览的扫描区块：活动扫描的状态与进度条（所有成员可见，多个目录时说第几个，不显示目录名称），或“没有正在运行的扫描”。诊断数不是进度。
 * 管理员可以从这里进入扫描目录与扫描详情；成员只能看状态。
 */
export function ScanSummary({ canManage }: { canManage: boolean }) {
  const scan = useActiveScan();
  return (
    <Panel title="扫描" action={canManage ? <Link to="/library">扫描目录</Link> : undefined}>
      <QueryContent query={scan} loading={<Skeleton label="正在加载扫描状态" lines={2} />}>
        {(active) => (active === null ? <p>没有正在运行的扫描。</p> : <ActiveScan scan={active} detailLink={canManage} />)}
      </QueryContent>
    </Panel>
  );
}
