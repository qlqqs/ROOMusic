import { useRouter } from "@tanstack/react-router";
import { Check, ChevronDown } from "lucide-react";
import { useMenu, type MenuAction } from "../../../shared/ui";
import { AGENT_SETTINGS_PATH } from "./settings-link";
import styles from "./thread.module.css";

type ModelMenuProps = {
  /** 当前会话用的模型；空字符串表示用设置里的模型（defaultModel）。 */
  current: string;
  defaultModel: string;
  /** 探测到的模型。 */
  models: readonly { id: string; display_name?: string }[];
  /** Steward 模式下的审查模型（C7）：空字符串表示与对话同一个模型；不是 Steward 模式时是 null，不标。 */
  reviewModel: string | null;
  disabled: boolean;
  onSelect: (model: string) => void;
};

/** 模型项右侧的说明：当前、正用作审查模型（Steward 模式），或模型的显示名。 */
function hintOf(model: { id: string; display_name?: string }, shown: string, reviewModel: string | null): string | null {
  const reviewing = reviewModel !== null && (reviewModel === "" ? model.id === shown : model.id === reviewModel);
  if (model.id === shown) return reviewing ? "当前，也用作审查模型" : "当前";
  if (reviewing) return "正用作审查模型";
  return model.display_name === undefined || model.display_name === model.id ? null : model.display_name;
}

/**
 * 输入框下的模型按钮：列出探测到的模型，切换只影响当前会话；Steward 模式下标出审查模型（没有另选时就是当前模型）；
 * 菜单最后是去设置更换接口的入口。当前模型不在探测结果里（手动填的）时也列在最前面。
 */
export function ModelMenu({ current, defaultModel, models, reviewModel, disabled, onSelect }: ModelMenuProps) {
  const router = useRouter();
  const shown = current === "" ? defaultModel : current;
  const ids = models.some((model) => model.id === shown) || shown === "" ? models : [{ id: shown }, ...models];
  const actions: MenuAction[] = [
    ...ids.map((model) => {
      const hint = hintOf(model, shown, reviewModel);
      return {
        id: `model:${model.id}`,
        label: model.id,
        icon: model.id === shown ? <Check size={14} /> : <span />,
        // 勾号只是图形；“当前”写在项名里，读屏也能知道。
        ...(hint === null ? {} : { hint }),
        group: "models",
        untitledGroup: true,
        onSelect: () => {
          if (model.id !== shown) onSelect(model.id);
        },
      };
    }),
    { id: "settings", label: "在设置里更换接口", group: "settings", untitledGroup: true, onSelect: () => void router.navigate({ to: AGENT_SETTINGS_PATH }) },
  ];
  const menu = useMenu({ label: "对话模型", actions, placement: "above" });
  return (
    <>
      <button type="button" className={styles.modelButton} {...menu.triggerProps} disabled={disabled} aria-label={`对话模型：${shown === "" ? "未选择" : shown}`}>
        <span>{shown === "" ? "选择模型" : shown}</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {menu.menu}
    </>
  );
}
