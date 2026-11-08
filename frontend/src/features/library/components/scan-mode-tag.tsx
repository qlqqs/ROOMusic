import { RefreshCw } from "lucide-react";
import { StatusTag } from "../../../shared/ui";
import type { ScanMode } from "../api/decoders";

/** 完整重扫的标签；增量扫描是常态，不显示。 */
export function ScanModeTag({ mode }: { mode: ScanMode }) {
  return mode === "full" ? <StatusTag icon={<RefreshCw size={14} />}>完整重扫</StatusTag> : null;
}
