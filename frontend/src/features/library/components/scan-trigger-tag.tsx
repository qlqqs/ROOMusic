import { Clock } from "lucide-react";
import { StatusTag } from "../../../shared/ui";
import type { ScanTrigger } from "../api/decoders";

/** 每天自动扫描发起的扫描的标签；手动开始是常态，不显示。 */
export function ScanTriggerTag({ trigger }: { trigger: ScanTrigger }) {
  return trigger === "scheduled" ? <StatusTag icon={<Clock size={14} />}>自动</StatusTag> : null;
}
