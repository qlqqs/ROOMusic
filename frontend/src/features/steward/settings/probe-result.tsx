import { formatDateTime } from "../../../shared/lib/time";
import { Button, FailureNotice, useMenu, type MenuAction } from "../../../shared/ui";
import { protocols, toolSupports, type ProbeResultDTO, type Protocol, type SettingsDTO, type ToolSupport } from "../api/decoders";
import type { SettingsPatch } from "../api/endpoints";
import { useProbePending, useUpdateSettings } from "../hooks/use-agent-settings";
import { failureKindLabel, probeStepLabels, protocolLabels, settingsFailureMessages, toolSupportLabels } from "./settings-messages";
import styles from "./settings.module.css";

type Overrides = NonNullable<SettingsPatch["overrides"]>;

/** 行尾的“修改”：显示两个字，读屏名称说明改的是哪一项。 */
function ModifyMenu({ label, actions, disabled }: { label: string; actions: readonly MenuAction[]; disabled: boolean }) {
  const menu = useMenu({ label, actions });
  return (
    <>
      <Button {...menu.triggerProps} aria-label={label} disabled={disabled}>
        修改
      </Button>
      {menu.menu}
    </>
  );
}

/**
 * 检测结果：时间、服务商，接口类型与调用工具两行（已保存的结论可以“修改”，写进 overrides；改过的标出“手动修改”，可以改回检测结果），
 * 以及每一步失败的原因。settings 为 null 时是这次没有通过的检测（报告没有保存），只读。
 * report 为 null：检测结论被后端清掉了（改了接口类型、换了地址或密钥，合同 4.2 规则 2），只剩“接口类型”一行，并提示重新检测。
 */
export function ProbeResult({ report, probedAt, settings }: { report: ProbeResultDTO | null; probedAt: string | null; settings: SettingsDTO | null }) {
  const update = useUpdateSettings();
  const probing = useProbePending();
  const overrides = settings?.overrides ?? {};
  const savedProtocol = settings === null || settings.protocol === "" ? undefined : settings.protocol;
  const protocol = overrides.protocol ?? report?.protocol ?? savedProtocol;
  const toolSupport = report === null ? null : (overrides.tool_support ?? report.tool_support);
  const disabled = update.isPending || probing;
  // overrides 给出时整体替换（合同 4.2）：在已有的覆盖项上改一项。
  const write = (next: Overrides) => update.mutate({ overrides: next });
  const without = (key: keyof Overrides): Overrides => {
    const next = { ...overrides };
    delete next[key];
    return next;
  };
  const protocolActions: MenuAction[] = [
    ...protocols.map((value: Protocol) => ({ id: value, label: protocolLabels[value], ...(value === protocol ? { hint: "当前" } : {}), onSelect: () => write({ ...overrides, protocol: value }) })),
    ...(overrides.protocol === undefined ? [] : [{ id: "reset", label: "改回检测结果", group: "reset", untitledGroup: true, onSelect: () => write(without("protocol")) }]),
  ];
  const toolActions: MenuAction[] = [
    ...toolSupports.map((value: ToolSupport) => ({ id: value, label: toolSupportLabels[value], ...(value === toolSupport ? { hint: "当前" } : {}), onSelect: () => write({ ...overrides, tool_support: value }) })),
    ...(overrides.tool_support === undefined ? [] : [{ id: "reset", label: "改回检测结果", group: "reset", untitledGroup: true, onSelect: () => write(without("tool_support")) }]),
  ];
  const heading = settings === null ? "这次检测的结果（没有保存）" : report === null || probedAt === null ? "还没有检测结果" : `检测结果，${formatDateTime(probedAt)}`;
  return (
    <section className={styles.probe} aria-label="检测结果" data-settings-flush="">
      <div className={styles.probeHead}>
        <span>{heading}</span>
        {report?.provider_name === undefined ? null : <span>{report.provider_name}</span>}
      </div>
      <dl className={styles.probeRows}>
        <div className={styles.probeRow}>
          <dt>接口类型</dt>
          <dd>
            {protocol === undefined ? "没有识别出来" : protocolLabels[protocol]}
            {overrides.protocol === undefined ? null : <span className={styles.muted}>手动修改</span>}
          </dd>
          {settings === null ? <span /> : <ModifyMenu label="修改接口类型" actions={protocolActions} disabled={disabled} />}
        </div>
        {toolSupport === null ? null : (
          <div className={styles.probeRow}>
            <dt>调用工具</dt>
            <dd>
              <span className={toolSupport === "unsupported" ? styles.bad : undefined}>{toolSupportLabels[toolSupport]}</span>
              {overrides.tool_support === undefined ? null : <span className={styles.muted}>手动修改</span>}
            </dd>
            {settings === null ? <span /> : <ModifyMenu label="修改调用工具" actions={toolActions} disabled={disabled} />}
          </div>
        )}
      </dl>
      {report?.failures === undefined || report.failures.length === 0 ? null : (
        <ul className={styles.failures} aria-label="没有通过的步骤">
          {report.failures.map((failure, index) => (
            <li key={index}>
              {probeStepLabels[failure.step]}
              {failure.protocol === undefined ? "" : `（${protocolLabels[failure.protocol]}）`}：{failureKindLabel(failure.error.kind)}
              {failure.error.status_code === undefined ? "" : `，接口返回 ${failure.error.status_code}`}
              {failure.error.message === "" ? "" : `：${failure.error.message}`}
            </li>
          ))}
        </ul>
      )}
      {settings !== null && report === null ? <p className={styles.note}>接口类型、地址或密钥改过，之前的检测结论已经作废。点“检测”重新确认这个接口能不能用、能不能调用工具。</p> : null}
      {settings !== null && toolSupport === "unsupported" ? (
        <p className={styles.note}>管家要靠调用工具才能查曲库。换一个支持工具调用的模型，或者在确定它支持时点“修改”。</p>
      ) : null}
      {settings !== null && toolSupport === "unconfirmed" && settings.model !== "" ? (
        <p className={styles.note}>还没有确认这个模型能调用工具。可以先用着；点“检测”会试调用一次。</p>
      ) : null}
      {update.isError ? <FailureNotice error={update.error} context="write" messages={settingsFailureMessages} title="没有修改成功" /> : null}
    </section>
  );
}
