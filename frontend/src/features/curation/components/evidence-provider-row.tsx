import { BookOpenCheck, Globe, MoreHorizontal } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { MenuButton, SettingRow } from "../../../shared/ui";
import { sourceName } from "../../governance";
import { useEvidenceCount } from "../hooks/use-curation";
import { useScopeRevert } from "../hooks/use-scope-revert";
import { ScopeRevertDialog, ScopeRevertResult, type ScopeRevertTarget } from "./scope-revert-dialog";
import styles from "./curation-sections.module.css";

// 模型依据库内写法或带引用的网页写入的修改（整理链 E）单独列出：它们没有外部源的结构佐证，最值得回头看一眼。
const PROVIDERS = [
  { id: "library_evidence", icon: <BookOpenCheck size={16} aria-hidden="true" /> },
  { id: "web_search", icon: <Globe size={16} aria-hidden="true" /> },
] as const;

/**
 * “依据库内或网页写入”一行：两种来源各写条数（操作记录按来源筛选的总数，含撤销记录），
 * 行尾菜单查看各自的改动或整批撤销（分段撤销，同外部源那一块）。
 */
export function EvidenceProviderRow({ refresh }: { refresh: () => void }) {
  const navigate = useNavigate();
  const revert = useScopeRevert(refresh);
  const [target, setTarget] = useState<ScopeRevertTarget | null>(null);
  const counts = { library_evidence: useEvidenceCount("library_evidence"), web_search: useEvidenceCount("web_search") };

  const actions = PROVIDERS.flatMap((provider) => {
    const name = sourceName(provider.id);
    return [
      { id: `view-${provider.id}`, label: `查看依据${name}的改动`, onSelect: () => void navigate({ to: "/settings/operations", search: { provider: provider.id } }) },
      {
        id: `revert-${provider.id}`,
        label: `撤销依据${name}的全部自动修改`,
        danger: true,
        onSelect: () => {
          revert.reset();
          setTarget({ scope: { kind: "provider", provider: provider.id }, name, sourceEnabled: true, total: counts[provider.id].data ?? null });
        },
      },
    ];
  });

  return (
    <>
      <SettingRow label="依据库内或网页写入" description="模型凭库内写法或带引用的网页写入的修改；它们不像外部源那样有曲目结构佐证。" plainLabel>
        <span className={styles.footerRow}>
          <span className={styles.value}>
            {PROVIDERS.map((provider, index) => {
              const count = counts[provider.id].data;
              const name = sourceName(provider.id);
              return (
                <span key={provider.id} className={styles.iconLabel} title={name} role="img" aria-label={`${name} ${count === undefined ? "读取中" : `${formatCount(count)} 条`}`}>
                  {index > 0 ? <span aria-hidden="true"> · </span> : null}
                  {provider.icon}
                  <span aria-hidden="true">{count === undefined ? "…" : formatCount(count)}</span>
                </span>
              );
            })}
          </span>
          <MenuButton label="依据库内或网页写入的更多操作" iconOnly icon={<MoreHorizontal size={18} />} actions={actions} />
        </span>
      </SettingRow>
      <ScopeRevertResult state={revert.state} onDismiss={revert.reset} />
      <ScopeRevertDialog
        target={target}
        state={revert.state}
        onConfirm={() => {
          if (target !== null) revert.start(target.scope);
        }}
        onResume={revert.resume}
        onCancel={() => {
          setTarget(null);
          if (revert.state.phase !== "running") revert.reset();
        }}
      />
    </>
  );
}
