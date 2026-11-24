import { Link } from "@tanstack/react-router";
import { Search, Sparkles } from "lucide-react";
import { useTooltip } from "../../../shared/ui";
import type { CurationSourceDTO } from "../api/decoders";
import styles from "./source-requirements.module.css";

type Requirement = NonNullable<CurationSourceDTO["requirements"]>[number];

const names: Record<Requirement["kind"], string> = { model: "对话模型", search: "搜索来源" };

function RequirementIcon({ requirement }: { requirement: Requirement }) {
  const name = names[requirement.kind];
  const label = requirement.ready ? `${name}已就绪` : `${name}没有设置，去管家设置`;
  const tip = useTooltip(label);
  const Icon = requirement.kind === "model" ? Sparkles : Search;
  return (
    <>
      <Link to="/settings/agent" className={styles.requirement} data-missing={requirement.ready ? undefined : ""} aria-label={label} {...tip.triggerProps}>
        <Icon size={15} aria-hidden="true" />
      </Link>
      {tip.tooltip}
    </>
  );
}

/**
 * 网络搜索依赖的两项（星光 = 对话模型，放大镜 = 搜索来源，都在打开自动整理的管理员的管家设置里）：就绪的图标正常显示，
 * 缺的变淡并带一个警示点；悬停说明缺什么，点击前往管家设置。
 */
export function SourceRequirements({ requirements }: { requirements: readonly Requirement[] }) {
  return (
    <span className={styles.requirements} role="list" aria-label="依赖">
      {requirements.map((requirement) => (
        <span key={requirement.kind} role="listitem">
          <RequirementIcon requirement={requirement} />
        </span>
      ))}
    </span>
  );
}

/** 依赖有缺：开关不可用（已经打开的仍可以关掉）。 */
export function requirementsMissing(source: CurationSourceDTO): boolean {
  return source.requirements?.some((requirement) => !requirement.ready) ?? false;
}
