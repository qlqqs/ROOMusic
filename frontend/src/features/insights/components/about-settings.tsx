import { useViewerScope } from "../../auth";
import { useBuildInfo } from "../../system";
import { QueryContent, SettingRow, SettingsGroup, Skeleton } from "../../../shared/ui";
import { useAbout } from "../hooks/use-insights";
import styles from "./insights.module.css";

const licenseNames: Record<string, string> = { "AGPL-3.0-only": "GNU Affero 通用公共许可证第 3 版（AGPL-3.0）" };

/** 设置 › 关于（所有人）：许可与源代码地址；管理员另外看到运行的版本。 */
export function AboutSettings() {
  const about = useAbout();
  const { viewer } = useViewerScope();
  return (
    <SettingsGroup title="关于 ROOMusic">
      <QueryContent query={about} loading={<Skeleton label="正在加载" lines={2} />}>
        {(data) => (
          <>
            {viewer.role === "admin" ? <VersionRow /> : null}
            <SettingRow label="许可" plainLabel>
              <span className={styles.value}>{licenseNames[data.license] ?? data.license}</span>
            </SettingRow>
            <SettingRow label="源代码" description="ROOMusic 是开源软件；修改后提供给他人使用时，需要以同一许可公开修改后的源代码。" plainLabel>
              <a href={data.source_url} target="_blank" rel="noreferrer">
                {data.source_url.replace(/^https?:\/\//, "")}
              </a>
            </SettingRow>
          </>
        )}
      </QueryContent>
    </SettingsGroup>
  );
}

function VersionRow() {
  const build = useBuildInfo();
  const version = build.data === undefined ? "—" : (build.data.version ?? "开发版本");
  return (
    <SettingRow label="版本" plainLabel>
      <span className={styles.value}>{version}</span>
    </SettingRow>
  );
}
