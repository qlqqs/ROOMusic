import { PencilLine } from "lucide-react";
import { StatusTag } from "../../../shared/ui";

/** 这个字段有人工修正：显示的是管理员改过的值，不是扫描值。文字加图标，不只靠颜色。 */
export function ManualCorrectionTag() {
  return (
    <StatusTag tone="accent" icon={<PencilLine size={12} />}>
      人工修正
    </StatusTag>
  );
}
