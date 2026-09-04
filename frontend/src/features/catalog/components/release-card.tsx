import { ReleaseCover } from "./release-cover";
import type { ReleaseCardModel } from "../model/display";

type ReleaseCardProps = {
  model: ReleaseCardModel;
  layout: "grid" | "list";
  onOpen: (releaseID: string) => void;
};

// 网格卡片与列表行共用同一 view model；卡片本身是单一按钮，不嵌套交互控件。
export function ReleaseCard({ model, layout, onOpen }: ReleaseCardProps) {
  return (
    <button
      className={layout === "grid" ? "release-card" : "release-row"}
      type="button"
      onClick={() => onOpen(model.id)}
      aria-label={`查看 ${model.title} 详情`}
    >
      <ReleaseCover artwork={model.artwork} title={model.title} className={layout === "grid" ? "card-cover" : "row-cover"} />
      <strong title={model.title}>{model.title}</strong>
      <span title={model.artistLabel}>{model.artistLabel}</span>
      <small>{model.yearLabel} · {model.sizeLabel}</small>
      {model.sourceLabel && <small>{model.sourceLabel}</small>}
      {model.attentionCount > 0 && <span className="attention-badge">需要检查 {model.attentionCount}</span>}
    </button>
  );
}
