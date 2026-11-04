import { useEffect, useRef } from "react";
import { useCatalogRefresh } from "../../catalog";
import type { ScanDTO } from "../api/decoders";

type Observed = { id: string; running: boolean; root: number | null } | null;

/** 正在处理的目录序号；计数阶段（另一遍遍历）与全部目录处理完之后都没有。 */
function processingRoot(scan: ScanDTO): number | null {
  if (scan.progress === undefined || scan.progress === null || scan.progress.phase === "counting") return null;
  return scan.root_progress?.index ?? null;
}

/**
 * 扫描把专辑写进资料库时让 catalog 的缓存（专辑列表、组与版本详情、概览的数字与“最近新增”）失效，已打开的页面借现有轮询看到新专辑：
 * - 观察到的运行中扫描结束（任一终态都可能已经提交了变化，不只是成功）、消失或换成了另一个；
 * - 同一个扫描从第 i 个目录换到更后面的目录：先扫完的目录里的专辑已经可以读到。计数阶段换目录不算；轮询间隔里跳过几个目录也只失效一次。
 * scan 为 undefined 表示还没读到，null 表示没有活动扫描；第一次读到时只记下来，不失效。
 */
export function useScanCatalogRefresh(scan: ScanDTO | null | undefined) {
  const refreshCatalog = useCatalogRefresh();
  const previous = useRef<Observed | undefined>(undefined);
  const current: Observed | undefined =
    scan === undefined ? undefined : scan === null ? null : { id: scan.id, running: scan.status === "running", root: processingRoot(scan) };
  const id = current?.id ?? null;
  const running = current?.running ?? false;
  const root = current?.root ?? null;
  const known = current !== undefined;

  useEffect(() => {
    if (!known) return;
    const before = previous.current;
    const now: Observed = id === null ? null : { id, running, root };
    previous.current = now;
    if (before === undefined || before === null || !before.running) return;
    const ended = now === null || now.id !== before.id || !now.running;
    const nextRoot = !ended && now.root !== null && before.root !== null && now.root > before.root;
    if (ended || nextRoot) void refreshCatalog();
  }, [known, id, running, root, refreshCatalog]);
}
