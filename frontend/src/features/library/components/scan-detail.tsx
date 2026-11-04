import { Link } from "@tanstack/react-router";
import { Ban } from "lucide-react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { isUuid } from "../../../shared/lib/ids";
import { formatDateTime, UNKNOWN_TIME } from "../../../shared/lib/time";
import { formatCount } from "../../../shared/lib/number";
import { Button, FailureNotice, Page, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import type { ScanDTO } from "../api/decoders";
import { useCancelScan } from "../hooks/use-scan-commands";
import { useScan } from "../hooks/use-scan";
import { isScanFinished, scanPhase, scanPhaseNotes } from "../scan-status";
import { scanFailureMessages } from "./root-messages";
import styles from "./scan-detail.module.css";
import { ScanDiagnostics } from "./scan-diagnostics";
import { ScanPhaseTag } from "./scan-phase-tag";

const backToLibrary = <Link to="/library">返回扫描目录</Link>;

function ScanNotFound() {
  return (
    <Page title="扫描不存在" actions={backToLibrary}>
      <p>请检查地址是否正确。</p>
    </Page>
  );
}

function ScanFacts({ scan }: { scan: ScanDTO }) {
  return (
    <dl className={styles.facts}>
      <dt>开始时间</dt>
      <dd>{formatDateTime(scan.started_at)}</dd>
      <dt>结束时间</dt>
      <dd>{scan.finished_at === null ? UNKNOWN_TIME : formatDateTime(scan.finished_at)}</dd>
      {scan.cancel_requested_at === null ? null : (
        <>
          <dt>请求取消</dt>
          <dd>{formatDateTime(scan.cancel_requested_at)}</dd>
        </>
      )}
      <dt>诊断</dt>
      <dd>{formatCount(scan.diagnostics.total)} 条</dd>
    </dl>
  );
}

function CancelScan({ scanId }: { scanId: string }) {
  const cancel = useCancelScan(scanId);
  const request = () => cancel.mutate();
  return (
    <div className={styles.cancel}>
      <div>
        <Button icon={<Ban size={16} />} busy={cancel.isPending} onClick={request}>
          {cancel.isPending ? "正在请求取消…" : "取消扫描"}
        </Button>
      </div>
      {cancel.isError ? (
        <FailureNotice error={cancel.error} context="write" messages={scanFailureMessages} action={<Button onClick={request}>重试</Button>} />
      ) : null}
    </div>
  );
}

function ScanStatusPanel({ scan }: { scan: ScanDTO }) {
  const phase = scanPhase(scan);
  return (
    <Panel title="状态">
      {/* 礼貌型 live region：只在阶段变化时播报一次，轮询得到相同的阶段不会重复播报。 */}
      <p role="status">
        <ScanPhaseTag phase={phase} />
      </p>
      <p>{scanPhaseNotes[phase]}</p>
      <ScanFacts scan={scan} />
      {phase === "running" ? <CancelScan scanId={scan.id} /> : null}
    </Panel>
  );
}

function ScanDetailContent({ scanId }: { scanId: string }) {
  const scan = useScan(scanId);
  if (scan.status === "error" && isHttpFailure(failureOf(scan.error), 404)) return <ScanNotFound />;
  const finished = scan.data !== undefined && isScanFinished(scan.data);
  return (
    <Page title="扫描详情" actions={backToLibrary}>
      <QueryContent query={scan} loading={<Skeleton label="正在加载扫描" lines={3} />}>
        {(data) => <ScanStatusPanel scan={data} />}
      </QueryContent>
      {scan.data === undefined ? null : <ScanDiagnostics scanId={scanId} finished={finished} />}
    </Page>
  );
}

/** 扫描详情：参数不是 UUID 时不发请求，直接显示“扫描不存在”；服务器返回 404 同样如此。 */
export function ScanDetail({ scanId }: { scanId: string }) {
  return isUuid(scanId) ? <ScanDetailContent scanId={scanId} /> : <ScanNotFound />;
}
