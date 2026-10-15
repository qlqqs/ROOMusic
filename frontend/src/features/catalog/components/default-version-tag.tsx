import { Bookmark } from "lucide-react";
import { StatusTag } from "../../../shared/ui";

/**
 * 这个版本是这张专辑生效中的默认版本（管理员指定的）：专辑卡片、打开专辑与版本导航里它排在最前。
 * 文字加图标，不只靠颜色；自动选择时没有任何版本带这个标签。
 */
export function DefaultVersionTag() {
  return (
    <StatusTag tone="outline" icon={<Bookmark size={12} />}>
      默认版本
    </StatusTag>
  );
}
