import { Link, useNavigate } from "@tanstack/react-router";
import { Play } from "lucide-react";
import { Button, FailureNotice, Notice, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { useActiveScan } from "../hooks/use-active-scan";
import { useStartScan } from "../hooks/use-scan-commands";
import { ActiveScan } from "./active-scan";
import { scanFailureMessages } from "./root-messages";
import styles from "./scan-section.module.css";

/**
 * 扫描目录页的“扫描”区段：有活动扫描时显示它并链接到详情，没有时提供“开始扫描”。
 * 新建的扫描（202）直接进入详情页；已有扫描在运行（200）时不新建，提示并留在本页，由活动扫描摘要里的链接进入。
 */
export function ScanSection() {
  const scope = useViewerScope();
  const navigate = useNavigate();
  const active = useActiveScan();
  const start = useStartScan();

  const begin = () =>
    start.mutate(undefined, {
      onSuccess: (response) => {
        if (!scope.isCurrent() || response.status !== 202) return;
        void navigate({ to: "/library/scans/$scanId", params: { scanId: response.data.id } });
      },
    });

  return (
    <Panel title="扫描">
      <QueryContent query={active} loading={<Skeleton label="正在加载扫描状态" lines={2} />}>
        {(scan) =>
          scan === null ? (
            <div className={styles.idle}>
              <p>没有正在运行的扫描。</p>
              <div>
                <Button variant="primary" icon={<Play size={16} />} busy={start.isPending} onClick={begin}>
                  {start.isPending ? "正在开始…" : "开始扫描"}
                </Button>
              </div>
            </div>
          ) : (
            <ActiveScan scan={scan} detailLink />
          )
        }
      </QueryContent>
      {start.isError ? (
        <FailureNotice
          error={start.error}
          context="write"
          messages={scanFailureMessages}
          action={<Button onClick={begin}>重试</Button>}
        />
      ) : null}
      {start.isSuccess && start.data.status === 200 ? (
        <Notice tone="info">
          已经有扫描在运行。{" "}
          <Link to="/library/scans/$scanId" params={{ scanId: start.data.data.id }}>
            查看这个扫描
          </Link>
          。
        </Notice>
      ) : null}
    </Panel>
  );
}
