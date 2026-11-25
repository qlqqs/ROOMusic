import { FailureNotice, Segmented, SettingRow, SettingsGroup, StatusTag } from "../../../shared/ui";
import { selectableModes, modeInfo } from "../components/mode-menu";
import type { SettingsDTO, StewardMode } from "../api/decoders";
import { useProbePending, useUpdateSettings } from "../hooks/use-agent-settings";
import { settingsFailureMessages } from "./settings-messages";

/**
 * 模式：新会话默认用哪一档。选中即保存（只提交 default_mode），说明跟着当前选中的一档变。
 * 已有的会话不受影响；输入框下的模式菜单（或 Shift+Tab）仍可为这一次会话另选。
 */
export function AgentDefaultMode({ settings }: { settings: SettingsDTO }) {
  const update = useUpdateSettings();
  // 检测会保存整份设置：检测进行中先不改，免得两次保存交错。
  const probing = useProbePending();
  const current = update.isPending && update.variables.default_mode !== undefined ? update.variables.default_mode : settings.default_mode;
  return (
    <SettingsGroup title="模式" status={update.isSuccess ? <StatusTag>已保存</StatusTag> : null}>
      <SettingRow label="新会话默认" description={`${modeInfo[current].description}。已有的会话不变，对话时也可以在输入框下另选。`}>
        {(ids) => (
          <Segmented<StewardMode>
            {...ids}
            options={selectableModes.map((mode) => ({ value: mode, label: modeInfo[mode].name }))}
            value={current}
            disabled={update.isPending || probing}
            onChange={(mode) => {
              if (mode !== settings.default_mode) update.mutate({ default_mode: mode });
            }}
          />
        )}
      </SettingRow>
      {update.isError ? <FailureNotice error={update.error} context="write" messages={settingsFailureMessages} title="默认模式没有保存" /> : null}
    </SettingsGroup>
  );
}
