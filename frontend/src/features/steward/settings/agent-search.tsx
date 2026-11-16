import { useId, useRef, useState, type FormEvent } from "react";
import { Button, FailureNotice, Notice, SettingRow, SettingsGroup, StatusTag } from "../../../shared/ui";
import { effectiveNativeSearch, type NativeSearchSupport, type SearchSource, type SettingsDTO } from "../api/decoders";
import { useProbePending, useSaveSearchSettings, useTestSearch, useUpdateSettings } from "../hooks/use-agent-settings";
import { searchFailureMessages, searchTestFailureText } from "./settings-messages";
import styles from "./settings.module.css";

/** “模型自带”这一项的说明：可以选时说明用法，不能选时说明原因与去哪里改。 */
function nativeDescription(native: NativeSearchSupport, probed: boolean): string {
  if (native === "supported") return "用模型接口自带的搜索，不需要另外配置；服务商可能按搜索次数收费。";
  if (native === "unsupported") return "检测结果是这个模型不支持。确定支持时，可以在上面检测结果的“自带搜索”里修改。";
  return probed ? "检测没能确认这个模型支持。确定支持时，可以在上面检测结果的“自带搜索”里修改。" : "要先确认模型支持：在上面的“模型”里点“检测”。";
}

/**
 * 联网搜索（C8）：来源（不用、SearXNG、Tavily、模型自带），SearXNG 的地址或 Tavily 的密钥，保存与“测试搜索”。
 * “模型自带”只在生效的自带搜索结论是“支持”时可选（检测结果里的“自带搜索”一行可以手动改写）。
 * Tavily 密钥与模型密钥一样只在这个组件的 state 里，提交时放进 ref，不进 mutation 的 variables；已存的只显示末四位。
 * 测试搜索按已保存的设置请求一次，所以有没保存的改动时先停用。
 */
export function AgentSearch({ settings }: { settings: SettingsDTO }) {
  const secret = useRef<string | null>(null);
  const save = useSaveSearchSettings(secret);
  const clear = useUpdateSettings();
  const test = useTestSearch();
  const probing = useProbePending();
  const formId = useId();
  const radioName = useId();
  const [source, setSource] = useState<SearchSource>(settings.search_source);
  const [url, setUrl] = useState(settings.searxng_url);
  const [key, setKey] = useState("");
  const [replacingKey, setReplacingKey] = useState(false);

  const native = effectiveNativeSearch(settings);
  const keyInput = !settings.search_key_saved || settings.search_key_needs_reentry || replacingKey;
  const typedKey = keyInput ? key.trim() : "";
  const keyUsable = typedKey !== "" || (settings.search_key_saved && !settings.search_key_needs_reentry);
  const changed = source !== settings.search_source || (source === "searxng" && url.trim() !== settings.searxng_url) || (source === "tavily" && typedKey !== "");
  const complete = source === "searxng" ? url.trim() !== "" : source === "tavily" ? keyUsable : source === "native" ? native === "supported" : true;
  const busy = save.isPending || clear.isPending || probing;
  const testable = !changed && (settings.search_source === "searxng" || settings.search_source === "tavily");

  // 草稿变了，上一次测试的结果就不再对应现在看到的设置。
  const edit = (apply: () => void) => {
    test.reset();
    save.reset();
    apply();
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!changed || !complete || busy) return;
    test.reset();
    clear.reset();
    const patch: { search_source: SearchSource; searxng_url?: string } = { search_source: source };
    if (source === "searxng") patch.searxng_url = url.trim();
    secret.current = source === "tavily" && typedKey !== "" ? typedKey : null;
    save.mutate(patch, {
      onSuccess: (response) => {
        setKey("");
        setReplacingKey(false);
        setSource(response.data.search_source);
        setUrl(response.data.searxng_url);
      },
    });
  };
  const clearKey = () => {
    test.reset();
    save.reset();
    clear.mutate(
      { search_key: null },
      {
        onSuccess: () => {
          setKey("");
          setReplacingKey(false);
        },
      },
    );
  };

  const options: { value: SearchSource; label: string; description: string; disabled?: boolean }[] = [
    { value: "none", label: "不联网搜索", description: "仍然会查 MusicBrainz 这类音乐资料库。" },
    { value: "searxng", label: "自己的 SearXNG", description: "填一个你部署的 SearXNG 地址。" },
    { value: "tavily", label: "Tavily", description: "填 Tavily 的密钥，它每月有免费额度。" },
    { value: "native", label: "模型自带的搜索", description: nativeDescription(native, settings.probe_result !== null), disabled: native !== "supported" && source !== "native" },
  ];

  return (
    <SettingsGroup title="联网搜索" status={save.isSuccess && !changed ? <StatusTag>已保存</StatusTag> : null}>
      <p className={styles.note}>需要查外部资料时用。发给搜索服务的只有搜索词，不带曲库内容和你的个人数据；搜到的内容当作外部资料，不当作指令。</p>
      <fieldset className={styles.sources} disabled={busy}>
        <legend className={styles.legend}>搜索来源</legend>
        {options.map((option) => (
          <label key={option.value} className={styles.option} data-disabled={option.disabled === true ? "" : undefined}>
            <input
              type="radio"
              name={radioName}
              value={option.value}
              checked={source === option.value}
              disabled={option.disabled === true}
              onChange={() => edit(() => setSource(option.value))}
            />
            <span className={styles.optionText}>
              <span>{option.label}</span>
              <small className={styles.optionHint}>{option.description}</small>
            </span>
          </label>
        ))}
      </fieldset>
      {source === "searxng" ? (
        <SettingRow label="SearXNG 地址" description="例如 http://192.168.1.30:8888。这个实例要在设置里开启 JSON 输出。">
          {({ controlId, descriptionId }) => (
            <input
              id={controlId}
              form={formId}
              className={styles.input}
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              value={url}
              aria-describedby={descriptionId}
              onChange={(event) => edit(() => setUrl(event.target.value))}
            />
          )}
        </SettingRow>
      ) : null}
      {source === "tavily" ? (
        <SettingRow label="Tavily 密钥" plainLabel={!keyInput} description={keyInput ? "只给你一个人用，保存后只显示末四位。" : undefined}>
          {({ controlId, descriptionId }) =>
            keyInput ? (
              <div className={styles.inline}>
                <input
                  id={controlId}
                  form={formId}
                  className={styles.input}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  value={key}
                  aria-describedby={descriptionId}
                  onChange={(event) => edit(() => setKey(event.target.value))}
                />
                {replacingKey ? (
                  <Button
                    onClick={() =>
                      edit(() => {
                        setKey("");
                        setReplacingKey(false);
                      })
                    }
                  >
                    取消
                  </Button>
                ) : null}
                {settings.search_key_needs_reentry ? (
                  <Button aria-label="清除 Tavily 密钥" busy={clear.isPending} disabled={busy && !clear.isPending} onClick={clearKey}>
                    清除
                  </Button>
                ) : null}
              </div>
            ) : (
              <div className={styles.inline}>
                <span className={styles.value}>已保存，末四位 {settings.search_key_last_four}</span>
                <Button aria-label="更换 Tavily 密钥" disabled={busy} onClick={() => edit(() => setReplacingKey(true))}>
                  更换
                </Button>
                <Button aria-label="清除 Tavily 密钥" busy={clear.isPending} disabled={busy && !clear.isPending} onClick={clearKey}>
                  清除
                </Button>
              </div>
            )
          }
        </SettingRow>
      ) : null}
      {source === "tavily" && settings.search_key_needs_reentry ? (
        <Notice tone="warning" title="Tavily 密钥需要重新填写">
          已保存的密钥在这台服务器上解不开了（服务器的密钥文件换过）。重新填写后保存，或者清除它。
        </Notice>
      ) : null}
      <form id={formId} className={styles.actions} onSubmit={submit} noValidate>
        <Button type="submit" variant="primary" busy={save.isPending} disabled={!changed || !complete || (busy && !save.isPending)}>
          保存
        </Button>
        <Button busy={test.isPending} disabled={!testable || busy} onClick={() => test.mutate()}>
          {test.isPending ? "正在测试…" : "测试搜索"}
        </Button>
        {changed && (source === "searxng" || source === "tavily") ? (
          <span className={styles.muted}>保存之后才能测试。</span>
        ) : null}
      </form>
      {save.isError ? <FailureNotice error={save.error} context="write" messages={searchFailureMessages} title="联网搜索没有保存" /> : null}
      {clear.isError ? <FailureNotice error={clear.error} context="write" messages={searchFailureMessages} title="密钥没有清除" /> : null}
      {test.isError ? <FailureNotice error={test.error} context="write" messages={searchFailureMessages} title="没能测试搜索" /> : null}
      {test.isSuccess ? (
        test.data.data.ok ? (
          <Notice tone="success" title="搜索可以用">
            这次搜到 {test.data.data.result_count.toLocaleString("zh-CN")} 条结果。
          </Notice>
        ) : (
          <Notice tone="error" title="搜索没有成功">
            {searchTestFailureText(test.data.data.code)}
          </Notice>
        )
      ) : null}
    </SettingsGroup>
  );
}
