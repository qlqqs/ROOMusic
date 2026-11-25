import { ArrowUpRight, Blocks, Check, Code, Copy, Download, MessageSquareWarning, Scale, ScrollText, Stethoscope } from "lucide-react";
import { useId, type ReactNode } from "react";
import { useViewerScope } from "../../auth";
import { useBuildInfo, useDiagnosticsExport } from "../../system";
import { FailureNotice, QueryContent, SettingsGroup, Skeleton, useTooltip, VisuallyHidden } from "../../../shared/ui";
import { useAbout } from "../hooks/use-insights";
import { projectLinks, type BuildLine } from "./project-links";
import styles from "./about-settings.module.css";

const ICON = 18;

/**
 * 设置 › 关于（所有人）：居中的标志、名称（管理员另见版本与 commit），下面两组列表：
 * 项目（源代码、更新说明、许可、第三方许可）与反馈（反馈问题，管理员另有复制诊断信息）。每行图标、短名称、右侧的值，整行可点。
 */
export function AboutSettings({ mark }: { mark: ReactNode }) {
  const about = useAbout();
  const { viewer } = useViewerScope();
  return (
    <QueryContent query={about} loading={<Skeleton label="正在加载" lines={3} />}>
      {(data) => (viewer.role === "admin" ? <AdminAbout mark={mark} sourceUrl={data.source_url} /> : <About mark={mark} sourceUrl={data.source_url} build={null} />)}
    </QueryContent>
  );
}

function AdminAbout({ mark, sourceUrl }: { mark: ReactNode; sourceUrl: string }) {
  const build = useBuildInfo();
  const diagnostics = useDiagnosticsExport();
  return <About mark={mark} sourceUrl={sourceUrl} build={build.data ?? null} buildPending={build.isPending} diagnostics={diagnostics} />;
}

type AboutProps = {
  mark: ReactNode;
  sourceUrl: string;
  /** null：看不到版本（成员），或管理员读取版本失败。 */
  build: BuildLine | null;
  buildPending?: boolean;
  diagnostics?: ReturnType<typeof useDiagnosticsExport>;
};

function About({ mark, sourceUrl, build, buildPending = false, diagnostics }: AboutProps) {
  const nameId = useId();
  const links = projectLinks(sourceUrl, build);
  const version = build?.version ?? null;
  return (
    <>
      <section className={styles.hero} aria-labelledby={nameId}>
        <span className={styles.plate} aria-hidden="true">
          {mark}
        </span>
        <h2 id={nameId} className={styles.name}>
          ROOMusic
        </h2>
        {build !== null ? <Build build={build} commitUrl={links.commit} /> : buildPending ? <span className={styles.buildPlaceholder} /> : null}
      </section>
      <SettingsGroup title="项目">
        <ul className={styles.list} data-settings-flush="">
          <LinkRow href={links.source} label="源代码" icon={links.releaseNotes !== null ? <GitHubMark /> : <Code size={ICON} aria-hidden="true" />} value={links.sourceLabel} />
          {links.releaseNotes === null ? null : (
            <LinkRow
              href={links.releaseNotes}
              label="更新说明"
              name={version === null ? "更新说明" : `${version} 的更新说明`}
              icon={<ScrollText size={ICON} aria-hidden="true" />}
              value={version}
            />
          )}
          <LinkRow href={links.license} label="许可" name="许可 AGPL-3.0" icon={<Scale size={ICON} aria-hidden="true" />} value="AGPL-3.0" />
          {links.thirdParty === null ? null : <LinkRow href={links.thirdParty} label="第三方许可" icon={<Blocks size={ICON} aria-hidden="true" />} />}
        </ul>
      </SettingsGroup>
      {links.feedback === null && diagnostics === undefined ? null : (
        <SettingsGroup
          title="反馈"
          footer={diagnostics === undefined || diagnostics.error === null ? undefined : <FailureNotice error={diagnostics.error} title="没有拿到诊断信息" />}
        >
          <ul className={styles.list} data-settings-flush="">
            {links.feedback === null ? null : <LinkRow href={links.feedback} label="反馈问题" icon={<MessageSquareWarning size={ICON} aria-hidden="true" />} value="GitHub Issues" />}
            {diagnostics === undefined ? null : <DiagnosticsRow diagnostics={diagnostics} />}
          </ul>
        </SettingsGroup>
      )}
    </>
  );
}

function Build({ build, commitUrl }: { build: BuildLine; commitUrl: string | null }) {
  const commit = build.commit?.slice(0, 7) ?? null;
  const tip = useTooltip(commit === null ? undefined : `commit ${commit}`);
  return (
    <p className={styles.build}>
      <span>
        <VisuallyHidden>版本 </VisuallyHidden>
        {build.version ?? "开发版本"}
      </span>
      {commit === null || commitUrl === null ? null : (
        <>
          <a className={styles.commit} href={commitUrl} target="_blank" rel="noreferrer" {...tip.triggerProps}>
            {commit}
          </a>
          {tip.tooltip}
        </>
      )}
    </p>
  );
}

/** 外部链接一行：新标签页打开，行尾斜箭头。name 是读屏名（包含可见的名称），缺省同 label。 */
function LinkRow({ href, label, name, icon, value = null }: { href: string; label: string; name?: string; icon: ReactNode; value?: string | null }) {
  return (
    <li>
      <a className={styles.row} href={href} target="_blank" rel="noreferrer" aria-label={name ?? label}>
        {icon}
        <span className={styles.label}>{label}</span>
        {value === null ? null : <span className={styles.value}>{value}</span>}
        <ArrowUpRight className={styles.trail} size={16} aria-hidden="true" />
      </a>
    </li>
  );
}

function DiagnosticsRow({ diagnostics }: { diagnostics: ReturnType<typeof useDiagnosticsExport> }) {
  const verbOf = (delivery: "copy" | "download") => (delivery === "copy" ? "复制" : "下载");
  const verb = verbOf(diagnostics.delivery);
  const label = diagnostics.done !== null ? `已${verbOf(diagnostics.done)}诊断信息` : diagnostics.pending ? `正在${verb}诊断信息` : `${verb}诊断信息`;
  const Trail = diagnostics.done !== null ? Check : diagnostics.delivery === "copy" ? Copy : Download;
  return (
    <li>
      <button
        type="button"
        className={styles.row}
        aria-busy={diagnostics.pending || undefined}
        aria-disabled={diagnostics.pending || undefined}
        onClick={diagnostics.run}
      >
        <Stethoscope size={ICON} aria-hidden="true" />
        <span className={styles.label}>{label}</span>
        <Trail className={styles.trail} data-done={diagnostics.done !== null ? "" : undefined} size={16} aria-hidden="true" />
      </button>
      <span className={styles.liveRegion} role="status">
        {diagnostics.done !== null ? label : ""}
      </span>
    </li>
  );
}

/** GitHub 的标志（lucide 不再提供品牌图标），用于指向 GitHub 仓库的源代码链接。 */
function GitHubMark() {
  return (
    <svg width={ICON} height={ICON} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}
