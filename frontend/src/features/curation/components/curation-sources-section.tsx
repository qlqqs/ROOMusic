import { MoreHorizontal } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { Fragment, useState } from "react";
import { FailureNotice, MenuButton, QueryContent, SettingRow, SettingsGroup, Skeleton, Switch } from "../../../shared/ui";
import { fetchOperations } from "../../governance/api/endpoints";
import type { CurationSettingsDTO, CurationSourceDTO } from "../api/decoders";
import { capabilitiesText, sourceInactiveText, writesOwnChanges } from "../curation-text";
import { useCurationSources, useSetCurationSource, useSetCurationSourceKey } from "../hooks/use-curation";
import { useScopeRevert } from "../hooks/use-scope-revert";
import { ScopeRevertDialog, ScopeRevertResult, type ScopeRevertTarget } from "./scope-revert-dialog";
import { keyLabel, SourceKeyRow } from "./source-key-row";
import styles from "./curation-sections.module.css";

/** 分组层面的说明：总开关关着、打开它的管理员不可用或已暂停时，所有源都不会被后台使用。 */
function groupNote(settings: CurationSettingsDTO | undefined): string | null {
  if (settings === undefined) return null;
  if (!settings.enabled) return "自动整理没有打开，后台不使用任何外部源。";
  if (settings.inactive_reason === "enabler_unavailable") return "打开自动整理的管理员已不可用，后台不使用任何外部源。";
  if (settings.paused) return "自动整理已暂停，后台暂时不使用外部源。";
  return null;
}

/** 一个源的说明：它提供什么，加上这一行自己不生效的原因；只用来识别的源说明结果记在哪里。 */
function sourceDescription(source: CurationSourceDTO): string {
  const parts = [capabilitiesText(source)];
  if (!writesOwnChanges(source)) parts.push("对上的结果记在 MusicBrainz 名下");
  const reason = sourceInactiveText(source.inactive_reason);
  return reason === null ? parts.join("；") : `${parts.join("；")}；${reason}`;
}

/**
 * “外部源”卡片：每个源一行，写它提供什么，开关即改即存；各行只写自己不生效的原因（总开关等共同原因写在分组层面）。
 * 行尾菜单：查看来自这个源的自动修改（操作记录按来源筛选）、撤销来自它的全部自动修改（分段撤销，见 ScopeRevertDialog）；
 * 只用来识别、自己不写入的源（AcoustID）没有这个菜单。需要密钥的源开着时，下面每把密钥一行（AcoustID 在发布镜像里只有用户密钥，没有内置应用密钥的构建另有应用密钥）。
 */
export function CurationSourcesSection({ settings, refresh }: { settings: CurationSettingsDTO | undefined; refresh: () => void }) {
  const sources = useCurationSources();
  const setSource = useSetCurationSource();
  const setKey = useSetCurationSourceKey();
  const revert = useScopeRevert(refresh);
  const navigate = useNavigate();
  const [target, setTarget] = useState<ScopeRevertTarget | null>(null);
  const note = groupNote(settings);

  const askRevert = (source: CurationSourceDTO) => {
    revert.reset();
    const base = { scope: { kind: "provider" as const, provider: source.id }, name: source.name, sourceEnabled: source.enabled };
    setTarget({ ...base, total: null });
    // 条数只用来说明影响范围；读不到时写“全部”。
    void fetchOperations({ page: 1, pageSize: 1, provider: source.id })
      .then((response) => setTarget((current) => (current !== null && current.scope.kind === "provider" && current.scope.provider === source.id ? { ...base, total: response.data.pagination.total } : current)))
      .catch(() => undefined);
  };

  return (
    <SettingsGroup
      title="外部源"
      footer={
        <>
          {setSource.isError ? <FailureNotice error={setSource.error} context="write" title="外部源的开关没有改成" /> : null}
          {setKey.isError ? <FailureNotice error={setKey.error} context="write" title={`${keyLabel(setKey.variables.name)}没有${setKey.variables.key === null ? "清除" : "保存"}`} /> : null}
          <ScopeRevertResult state={revert.state} onDismiss={revert.reset} />
        </>
      }
    >
      {note === null ? null : <p className={styles.groupNote}>{note}</p>}
      <QueryContent query={sources} loading={<Skeleton label="正在加载外部源" lines={3} />}>
        {(data) => (
          <>
            {data.items.map((source) => {
              const checked = setSource.isPending && setSource.variables.id === source.id ? setSource.variables.enabled : source.enabled;
              const actions = [
                {
                  id: "view",
                  label: `查看来自 ${source.name} 的改动`,
                  onSelect: () => void navigate({ to: "/settings/operations", search: { provider: source.id } }),
                },
                { id: "revert", label: `撤销来自 ${source.name} 的全部自动修改`, danger: true, onSelect: () => askRevert(source) },
              ];
              return (
                <Fragment key={source.id}>
                  <SettingRow label={source.name} description={sourceDescription(source)}>
                    {({ controlId, descriptionId }) => (
                      <span className={styles.footerRow}>
                        <Switch
                          id={controlId}
                          aria-describedby={descriptionId}
                          checked={checked}
                          disabled={setSource.isPending}
                          onChange={(enabled) => setSource.mutate({ id: source.id, enabled })}
                        />
                        {writesOwnChanges(source) ? (
                          <MenuButton label={`${source.name} 的更多操作`} iconOnly icon={<MoreHorizontal size={18} />} actions={actions} />
                        ) : (
                          <span className={styles.menuSpacer} aria-hidden="true" />
                        )}
                      </span>
                    )}
                  </SettingRow>
                  {checked
                    ? source.keys?.map((sourceKey) => (
                        <SourceKeyRow
                          key={sourceKey.name}
                          source={source}
                          sourceKey={sourceKey}
                          busy={setKey.isPending && setKey.variables.id === source.id && setKey.variables.name === sourceKey.name}
                          onSave={(key, done) => setKey.mutate({ id: source.id, name: sourceKey.name, key }, { onSuccess: done })}
                        />
                      ))
                    : null}
                </Fragment>
              );
            })}
          </>
        )}
      </QueryContent>
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
    </SettingsGroup>
  );
}
