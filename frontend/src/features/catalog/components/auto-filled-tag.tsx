import { Wand } from "lucide-react";
import { StatusTag } from "../../../shared/ui";

/** 这个字段显示的是自动整理补上的值（来自外部资料），不是扫描值；人工修正之后改显示“人工修正”。文字加图标（魔术棒，常见的“自动处理”标识；星光留给管家），不只靠颜色。 */
export function AutoFilledTag() {
  return (
    <StatusTag tone="neutral" icon={<Wand size={12} />}>
      自动补全
    </StatusTag>
  );
}
