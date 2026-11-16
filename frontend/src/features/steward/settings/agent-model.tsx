import { useId, useRef, useState, type FormEvent } from "react";
import { describeFailure, errorBodyOf, failureOf } from "../../../shared/api";
import { Button, FailureNotice, Notice, SettingRow, SettingsGroup } from "../../../shared/ui";
import { decodeProbeFailure, type ProbeResultDTO, type SettingsDTO } from "../api/decoders";
import { useProbePending, useProbeSettings, useUpdateSettings } from "../hooks/use-agent-settings";
import { ProbeResult } from "./probe-result";
import { EGRESS_DENIED_TEXT, settingsFailureMessages } from "./settings-messages";
import styles from "./settings.module.css";

/** 这次检测没有通过时的探测报告（没有存进设置）。 */
type FailedProbe = { report: ProbeResultDTO; probedAt: string };

function failedProbeOf(error: unknown): FailedProbe | null {
  try {
    const decoded = decodeProbeFailure(errorBodyOf(error));
    return { report: decoded.probe_result, probedAt: decoded.probed_at };
  } catch {
    return null;
  }
}

/** 检测失败的说明：接口错误码按合同 4.3 的分类；模型端点的失败用后端按分类给的固定说明。 */
function probeFailureText(error: unknown): string {
  const failure = failureOf(error);
  if (failure === null) return "检测没能完成，请重试。";
  if (failure.kind !== "http") return describeFailure(failure, { context: "write" }).message;
  switch (failure.code) {
    case "egress_denied":
      return EGRESS_DENIED_TEXT;
    case "provider_timeout":
      return "模型接口在时限内没有完成检测，请稍后重试。";
    case "provider_unreachable":
      return failure.message === "" ? "连不上模型接口。" : `${failure.message}。`;
    case "steward_not_configured":
      return "先填接口地址；已保存的密钥解不开时请重新填写。";
    default:
      return describeFailure(failure, { context: "write", messages: settingsFailureMessages }).message;
  }
}

/** 主机与端口（主机名转小写、去掉末尾的点，未写端口时 http 取 80、https 取 443），与后端判断“换了地址”的规则一致；解析不了时是原文。 */
export function authorityOf(endpoint: string): string {
  try {
    const url = new URL(endpoint);
    const port = url.port !== "" ? url.port : url.protocol === "https:" ? "443" : "80";
    return `${url.hostname.toLowerCase().replace(/^\[|\]$/g, "").replace(/\.$/, "")}:${port}`;
  } catch {
    return endpoint;
  }
}

function requestIdOf(error: unknown): string | null {
  const failure = failureOf(error);
  return failure === null || failure.kind === "aborted" ? null : failure.requestId;
}

/**
 * 模型：接口地址、密钥与“检测”（探测接口，成功即保存；失败时显示每一步的原因，原设置不变），检测结果与手动修改，对话模型。
 * 密钥只在这个组件的 state 里，不写进任何存储；检测成功或保存后清空输入框。已存的密钥只显示末四位。
 */
export function AgentModel({ settings }: { settings: SettingsDTO }) {
  // 这次检测要带的密钥：只在提交时放进来，检测结束就清掉（不进 mutation 的 variables）。
  const secret = useRef<string | null>(null);
  const probe = useProbeSettings(secret);
  const probing = useProbePending();
  const update = useUpdateSettings();
  const modelListId = useId();
  const [endpoint, setEndpoint] = useState(settings.endpoint);
  const [key, setKey] = useState("");
  const [replacingKey, setReplacingKey] = useState(false);
  const [model, setModel] = useState(settings.model);
  const [failed, setFailed] = useState<FailedProbe | null>(null);
  // 已存的密钥只发给原来的主机与端口：地址换了主机或端口，后端会清掉它（合同 4.2 规则 1），所以要重新填写或留空。
  const hostChanged = settings.key_saved && authorityOf(endpoint.trim()) !== authorityOf(settings.endpoint);
  const keyInput = !settings.key_saved || settings.key_needs_reentry || replacingKey || hostChanged;
  const busy = probing || update.isPending;

  const runProbe = (event: FormEvent) => {
    event.preventDefault();
    setFailed(null);
    update.reset();
    const input: { endpoint: string; model?: string } = { endpoint: endpoint.trim() };
    if (model.trim() !== "") input.model = model.trim();
    secret.current = keyInput && key.trim() !== "" ? key.trim() : null;
    probe.mutate(input, {
      onSuccess: (response) => {
        setKey("");
        setReplacingKey(false);
        setEndpoint(response.data.endpoint);
        setModel(response.data.model);
      },
      onError: (error) => setFailed(failedProbeOf(error)),
    });
  };
  const saveModel = (event: FormEvent) => {
    event.preventDefault();
    probe.reset();
    setFailed(null);
    update.mutate({ model: model.trim() }, { onSuccess: (response) => setModel(response.data.model) });
  };
  const clearKey = () => {
    probe.reset();
    setFailed(null);
    update.mutate(
      { key: null },
      {
        onSuccess: () => {
          setKey("");
          setReplacingKey(false);
        },
      },
    );
  };

  const models = settings.probe_result?.models ?? [];
  const showResult = failed !== null || settings.probe_result !== null || settings.protocol !== "" || Object.keys(settings.overrides).length > 0;
  return (
    <SettingsGroup title="模型">
      <p className={styles.note}>填你自己的接口地址和密钥。它们只给你一个人用，其他成员和管理员都看不到。</p>
      {settings.key_needs_reentry ? (
        <Notice tone="warning" title="密钥需要重新填写">
          已保存的密钥在这台服务器上解不开了（服务器的密钥文件换过）。重新填写后点“检测”，或者清除它；其余设置都还在。
        </Notice>
      ) : null}
      <SettingRow label="接口地址" description="例如 https://api.deepseek.com，或局域网里的 http://192.168.1.20:11434。">
        {({ controlId, descriptionId }) => (
          <form id={`${modelListId}-probe`} className={styles.inline} onSubmit={runProbe} noValidate>
            <input
              id={controlId}
              className={styles.input}
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              value={endpoint}
              aria-describedby={descriptionId}
              onChange={(event) => setEndpoint(event.target.value)}
            />
            <Button type="submit" variant="primary" busy={probing} disabled={endpoint.trim() === "" || update.isPending}>
              {probing ? "正在检测…" : "检测"}
            </Button>
          </form>
        )}
      </SettingRow>
      <SettingRow
        label="密钥"
        plainLabel={!keyInput}
        description={
          !keyInput ? undefined : hostChanged ? "换了地址要重新填写密钥：原来的密钥只发给原来的地址。留空表示这个地址不需要密钥。" : "没有密钥就留空。填了要点“检测”才会保存。"
        }
      >
        {({ controlId, descriptionId }) =>
          keyInput ? (
            <div className={styles.inline}>
              <input
                id={controlId}
                form={`${modelListId}-probe`}
                className={styles.input}
                type="password"
                autoComplete="off"
                spellCheck={false}
                placeholder="没有密钥就留空"
                value={key}
                aria-describedby={descriptionId}
                onChange={(event) => setKey(event.target.value)}
              />
              {replacingKey && !hostChanged ? (
                <Button
                  onClick={() => {
                    setKey("");
                    setReplacingKey(false);
                  }}
                >
                  取消
                </Button>
              ) : null}
              {settings.key_needs_reentry ? (
                <Button busy={update.isPending} disabled={probing} onClick={clearKey}>
                  清除
                </Button>
              ) : null}
            </div>
          ) : (
            <div className={styles.inline}>
              <span className={styles.value}>已保存，末四位 {settings.key_last_four}</span>
              <Button disabled={busy} onClick={() => setReplacingKey(true)}>
                更换
              </Button>
              <Button busy={update.isPending} disabled={probing} onClick={clearKey}>
                清除
              </Button>
            </div>
          )
        }
      </SettingRow>
      {probe.isError ? (
        <Notice tone="error" title="检测没有通过，原来的设置没有改动" requestId={requestIdOf(probe.error)}>
          {probeFailureText(probe.error)}
        </Notice>
      ) : null}
      {!showResult ? null : failed !== null ? (
        <ProbeResult report={failed.report} probedAt={failed.probedAt} settings={null} />
      ) : (
        <ProbeResult report={settings.probe_result} probedAt={settings.probed_at} settings={settings} />
      )}
      <SettingRow label="对话模型" description={models.length > 0 ? "从检测到的模型里选，也可以直接填写。" : "先检测接口，再从检测到的模型里选；也可以直接填写。"}>
        {({ controlId, descriptionId }) => (
          <form className={styles.inline} onSubmit={saveModel}>
            <input
              id={controlId}
              className={styles.input}
              list={modelListId}
              autoComplete="off"
              spellCheck={false}
              value={model}
              aria-describedby={descriptionId}
              onChange={(event) => setModel(event.target.value)}
            />
            <datalist id={modelListId}>
              {models.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.display_name}
                </option>
              ))}
            </datalist>
            <Button type="submit" variant="primary" busy={update.isPending} disabled={model.trim() === settings.model || probing}>
              保存
            </Button>
          </form>
        )}
      </SettingRow>
      {update.isError ? <FailureNotice error={update.error} context="write" messages={settingsFailureMessages} title="没有保存" /> : null}
    </SettingsGroup>
  );
}
