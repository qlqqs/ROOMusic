import { isFieldAutoFilled, isFieldCorrected, type FieldMarks } from "../format";
import { AutoFilledTag } from "./auto-filled-tag";
import { ManualCorrectionTag } from "./manual-correction-tag";

/** 字段的来源标记：人工修正优先，其次是自动补全；都不是时不显示。 */
export function FieldMarkTag({ marks, field }: { marks: FieldMarks; field: string }) {
  if (isFieldCorrected(marks.corrected, field)) return <ManualCorrectionTag />;
  if (isFieldAutoFilled(marks.auto, marks.corrected, field)) return <AutoFilledTag />;
  return null;
}

/** 字段有没有来源标记（决定要不要留出标记的位置）。 */
export function hasFieldMark(marks: FieldMarks, field: string): boolean {
  return isFieldCorrected(marks.corrected, field) || isFieldAutoFilled(marks.auto, marks.corrected, field);
}
