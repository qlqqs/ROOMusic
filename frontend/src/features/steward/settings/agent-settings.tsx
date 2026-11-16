import { Settings } from "lucide-react";
import { EmptyState, FailureNotice, Skeleton } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { AccessTokens } from "../components/access-tokens";
import { DEFAULT_STEWARD_NAME, isStewardDisabled, useStewardSettings } from "../hooks/use-steward-settings";
import { AgentMemories } from "./agent-memories";
import { AgentModel } from "./agent-model";
import { AgentName } from "./agent-name";
import { AgentSearch } from "./agent-search";
import { MembersSection } from "./members-section";
import styles from "./settings.module.css";

/**
 * 设置 › Agent 的内容：名字、模型（含审查模型，C7）、联网搜索（C8）、记忆（C10）、访问令牌，管理员另有“成员”一段（成员开关与内网地址放行）。所有登录用户都有这一栏。
 * 成员开关关闭时成员看到说明，名字、模型与联网搜索不显示；记忆（C10）与访问令牌两组保留：记忆可以查看与删除、不能添加与修改，
 * 令牌仍能查看与吊销、不能新建（C3）。
 */
export function AgentSettings() {
  const { viewer } = useViewerScope();
  const settings = useStewardSettings();
  const disabled = isStewardDisabled(settings.error);
  return (
    <div className={styles.sections}>
      {disabled ? (
        <EmptyState icon={<Settings size={24} />} title="管理员暂时关闭了这个功能">
          成员现在不能使用{DEFAULT_STEWARD_NAME}，名字和模型设置也暂时用不了。需要时请联系管理员在设置里打开。
        </EmptyState>
      ) : settings.data === undefined ? (
        settings.isError ? <FailureNotice error={settings.error} title="设置没能读出来" /> : <Skeleton label="正在读取设置" lines={4} />
      ) : (
        <>
          <AgentName settings={settings.data} />
          <AgentModel settings={settings.data} />
          <AgentSearch settings={settings.data} />
        </>
      )}
      <AgentMemories readOnly={disabled} />
      <AccessTokens />
      {viewer.role === "admin" ? <MembersSection /> : null}
    </div>
  );
}
