import { CircleAlert, CircleCheck, PlugZap } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { Button, FailureNotice, QueryContent, Segmented, SettingRow, SettingsGroup, Skeleton, StatusTag, Switch } from "../../../shared/ui";
import type { ProxyScheme, ProxySettingsDTO, ProxyTestCode } from "../api/decoders";
import { useProxySettings, useSaveProxySettings, useTestProxy } from "../hooks/use-proxy";
import styles from "./proxy-settings.module.css";

const schemeOptions = [
  { value: "http", label: "HTTP" },
  { value: "socks5", label: "SOCKS5" },
] as const satisfies readonly { value: ProxyScheme; label: string }[];

const testFailureText: Record<ProxyTestCode, string> = {
  proxy_unreachable: "连不上代理服务器，检查地址和端口。",
  proxy_auth_failed: "代理拒绝了用户名或密码。",
  target_unreachable: "代理连上了，但通过它访问不到 MusicBrainz。",
  timeout: "10 秒内没有收到响应。",
};

const saveFailures = { version_conflict: "设置刚在别处改过，刷新页面后再改。" } as const;

/** 端口输入：只接受 1–65535 的整数；空串与其它输入都算没填。 */
function parsePort(text: string): number | null {
  if (!/^\d{1,5}$/.test(text.trim())) return null;
  const port = Number(text.trim());
  return port >= 1 && port <= 65535 ? port : null;
}

/** 设置 › 网络（管理员）：全局出站代理。 */
export function ProxySettings() {
  const settings = useProxySettings();
  return (
    <QueryContent query={settings} loading={<Skeleton label="正在加载代理设置" lines={4} />}>
      {(data) => <ProxyForm settings={data} />}
    </QueryContent>
  );
}

/**
 * 草稿表单：保存成功后按接口返回的设置重置草稿（保留“已保存”状态）；保存带读到时的 version，别处改过得到 409。
 * 密码与模型密钥一样只在组件 state 里，提交或测试时放进 ref，不进 mutation 的 variables；已保存的只显示“已保存”。
 * 测试按当前草稿发起，不必先保存；没改过密码时后端用已存的。
 */
function ProxyForm({ settings }: { settings: ProxySettingsDTO }) {
  const formId = useId();
  const saveSecret = useRef<string | null>(null);
  const testSecret = useRef<string | null>(null);
  const save = useSaveProxySettings(saveSecret);
  const test = useTestProxy(testSecret);
  const [enabled, setEnabled] = useState(settings.enabled);
  const [scheme, setScheme] = useState<ProxyScheme>(settings.scheme);
  const [host, setHost] = useState(settings.host);
  const [portText, setPortText] = useState(settings.port === null ? "" : String(settings.port));
  const [username, setUsername] = useState(settings.username);
  const [password, setPassword] = useState("");
  const [replacingPassword, setReplacingPassword] = useState(!settings.password_set);
  const [clearPassword, setClearPassword] = useState(false);

  const port = parsePort(portText);
  const typedPassword = replacingPassword ? password : "";
  const passwordChanged = clearPassword || typedPassword !== "";
  const changed =
    enabled !== settings.enabled ||
    scheme !== settings.scheme ||
    host.trim() !== settings.host ||
    port !== settings.port ||
    username.trim() !== settings.username ||
    passwordChanged;
  const addressComplete = host.trim() !== "" && port !== null;
  const portError = portText.trim() !== "" && port === null ? "端口是 1 到 65535 的整数。" : null;
  const savable = changed && (!enabled || addressComplete) && portError === null;
  const busy = save.isPending || test.isPending;

  // 草稿变了，上一次测试和保存的结果就不再对应现在看到的设置。
  const edit = (apply: () => void) => {
    test.reset();
    save.reset();
    apply();
  };
  const draft = () => ({ scheme, host: host.trim(), port, username: username.trim() });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!savable || busy) return;
    test.reset();
    saveSecret.current = clearPassword ? "" : typedPassword !== "" ? typedPassword : null;
    save.mutate(
      { draft: { enabled, ...draft() }, version: settings.version },
      {
        onSuccess: ({ data }) => {
          setEnabled(data.enabled);
          setScheme(data.scheme);
          setHost(data.host);
          setPortText(data.port === null ? "" : String(data.port));
          setUsername(data.username);
          setPassword("");
          setReplacingPassword(!data.password_set);
          setClearPassword(false);
        },
      },
    );
  };
  const runTest = () => {
    if (!addressComplete || busy) return;
    save.reset();
    testSecret.current = typedPassword !== "" ? typedPassword : null;
    test.mutate(draft());
  };

  return (
    <SettingsGroup
      title="代理"
      status={save.isSuccess && !changed ? <StatusTag>已保存</StatusTag> : null}
      footer={save.isError ? <FailureNotice error={save.error} context="write" messages={saveFailures} title="代理设置没有保存" /> : undefined}
    >
      <SettingRow label="通过代理访问外网" description="元数据、封面、歌词、艺人资料、模型接口和网页读取都走这个代理；局域网和本机地址始终直连。">
        {({ controlId, descriptionId }) => <Switch id={controlId} aria-describedby={descriptionId} checked={enabled} disabled={busy} onChange={(next) => edit(() => setEnabled(next))} />}
      </SettingRow>
      {enabled ? (
        <>
          <SettingRow label="类型" plainLabel nested>
            {({ labelId }) => <Segmented options={schemeOptions} value={scheme} labelId={labelId} disabled={busy} onChange={(next) => edit(() => setScheme(next))} />}
          </SettingRow>
          <SettingRow label="地址" plainLabel nested description={portError ?? "主机名或 IP，加端口，例如 192.168.1.2 和 7890。"}>
            {({ labelId, descriptionId }) => (
              <div className={styles.address} role="group" aria-labelledby={labelId}>
                <input
                  form={formId}
                  className={styles.input}
                  aria-label="代理主机"
                  aria-describedby={descriptionId}
                  autoComplete="off"
                  spellCheck={false}
                  value={host}
                  disabled={busy}
                  onChange={(event) => edit(() => setHost(event.target.value))}
                />
                <input
                  form={formId}
                  className={styles.port}
                  aria-label="代理端口"
                  aria-invalid={portError === null ? undefined : true}
                  aria-describedby={descriptionId}
                  inputMode="numeric"
                  autoComplete="off"
                  value={portText}
                  disabled={busy}
                  onChange={(event) => edit(() => setPortText(event.target.value))}
                />
              </div>
            )}
          </SettingRow>
          <SettingRow label="用户名" nested description="代理不需要认证就留空。">
            {({ controlId, descriptionId }) => (
              <input
                id={controlId}
                form={formId}
                className={styles.input}
                aria-describedby={descriptionId}
                autoComplete="off"
                spellCheck={false}
                value={username}
                disabled={busy}
                onChange={(event) => edit(() => setUsername(event.target.value))}
              />
            )}
          </SettingRow>
          <SettingRow label="密码" nested plainLabel={!replacingPassword}>
            {({ controlId }) =>
              replacingPassword ? (
                <div className={styles.inline}>
                  <input
                    id={controlId}
                    form={formId}
                    className={styles.input}
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    disabled={busy}
                    onChange={(event) => edit(() => setPassword(event.target.value))}
                  />
                  {settings.password_set ? (
                    <Button
                      disabled={busy}
                      onClick={() =>
                        edit(() => {
                          setPassword("");
                          setReplacingPassword(false);
                        })
                      }
                    >
                      取消
                    </Button>
                  ) : null}
                </div>
              ) : (
                <div className={styles.inline}>
                  <span className={styles.value}>{clearPassword ? "保存后清除" : "已设置"}</span>
                  {clearPassword ? (
                    <Button disabled={busy} onClick={() => edit(() => setClearPassword(false))}>
                      撤回
                    </Button>
                  ) : (
                    <>
                      <Button aria-label="更换代理密码" disabled={busy} onClick={() => edit(() => setReplacingPassword(true))}>
                        更换
                      </Button>
                      <Button aria-label="清除代理密码" disabled={busy} onClick={() => edit(() => setClearPassword(true))}>
                        清除
                      </Button>
                    </>
                  )}
                </div>
              )
            }
          </SettingRow>
        </>
      ) : null}
      <form id={formId} className={styles.actions} onSubmit={submit} noValidate>
        <Button type="submit" variant="primary" busy={save.isPending} disabled={!savable || (busy && !save.isPending)}>
          {save.isPending ? "正在保存…" : "保存"}
        </Button>
        {enabled ? (
          <Button variant="icon" icon={<PlugZap size={16} aria-hidden="true" />} label="测试连接" tooltip="按现在填的地址试连一次 MusicBrainz，不必先保存" busy={test.isPending} disabled={!addressComplete || (busy && !test.isPending)} onClick={runTest} />
        ) : null}
        <TestResult test={test} />
      </form>
    </SettingsGroup>
  );
}

function TestResult({ test }: { test: ReturnType<typeof useTestProxy> }) {
  if (test.isPending) return <span className={styles.muted} role="status">正在测试…</span>;
  if (test.isError) return <FailureNotice error={test.error} context="write" title="没能测试代理" />;
  if (!test.isSuccess) return null;
  const result = test.data.data;
  if (result.ok)
    return (
      <span className={styles.result} role="status">
        <CircleCheck size={14} aria-hidden="true" />
        {result.latency_ms === null ? "代理可以用" : `代理可以用，用时 ${result.latency_ms.toLocaleString("zh-CN")} 毫秒`}
      </span>
    );
  return (
    <span className={styles.failure} role="status">
      <CircleAlert size={14} aria-hidden="true" />
      {result.code === null ? "代理没有连通。" : testFailureText[result.code]}
    </span>
  );
}
