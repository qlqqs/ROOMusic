import { useState, type FormEvent } from "react";
import { Button, FailureNotice, SettingRow, SettingsGroup, StatusTag } from "../../../shared/ui";
import { MAX_DISPLAY_NAME, type SettingsDTO } from "../api/decoders";
import { useProbePending, useUpdateSettings } from "../hooks/use-agent-settings";
import { DEFAULT_STEWARD_NAME } from "../hooks/use-steward-settings";
import { settingsFailureMessages } from "./settings-messages";
import styles from "./settings.module.css";

/** 名字的长度按 Unicode 字符计（与后端的 20 字上限一致）。 */
const characters = (text: string) => [...text].length;

/**
 * 名字：保存时只提交 display_name。成功后设置查询直接换成响应，侧栏、标签栏、面板标题与输入框提示不刷新就变。
 * 留空或“恢复为管家”都表示用默认名字。
 */
export function AgentName({ settings }: { settings: SettingsDTO }) {
  const update = useUpdateSettings();
  // 检测会保存整份设置：检测进行中先不改名，免得两次保存交错。
  const probing = useProbePending();
  const [value, setValue] = useState(settings.display_name);
  const trimmed = value.trim();
  const tooLong = characters(trimmed) > MAX_DISPLAY_NAME;
  const unchanged = trimmed === settings.display_name;
  const save = (name: string) => update.mutate({ display_name: name }, { onSuccess: (response) => setValue(response.data.display_name) });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!tooLong && !unchanged) save(trimmed);
  };
  return (
    <SettingsGroup title="名字" status={update.isSuccess && unchanged ? <StatusTag>已保存</StatusTag> : null}>
      <SettingRow label="名字" description={`侧栏、对话面板和输入框里都用这个名字称呼它，它自己也知道。只对你自己生效，留空就叫“${DEFAULT_STEWARD_NAME}”。`}>
        {({ controlId, descriptionId }) => (
          <form className={styles.inline} onSubmit={submit}>
            <input
              id={controlId}
              className={styles.input}
              value={value}
              placeholder={DEFAULT_STEWARD_NAME}
              aria-describedby={descriptionId}
              aria-invalid={tooLong ? true : undefined}
              onChange={(event) => {
                update.reset();
                setValue(event.target.value);
              }}
            />
            <Button type="submit" variant="primary" busy={update.isPending} disabled={tooLong || unchanged || probing}>
              保存
            </Button>
            {settings.display_name === "" ? null : (
              <Button disabled={update.isPending || probing} onClick={() => save("")}>
                恢复为“{DEFAULT_STEWARD_NAME}”
              </Button>
            )}
            {tooLong ? <p className={styles.fieldError}>最多 {MAX_DISPLAY_NAME} 个字。</p> : null}
          </form>
        )}
      </SettingRow>
      {update.isError ? <FailureNotice error={update.error} context="write" messages={settingsFailureMessages} title="名字没有保存" /> : null}
    </SettingsGroup>
  );
}
