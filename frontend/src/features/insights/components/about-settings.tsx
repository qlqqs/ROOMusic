import { Blocks, Check, Code, MessageSquareWarning, Scale, ScrollText, Stethoscope } from "lucide-react";
import { useId, type ReactNode } from "react";
import { useViewerScope } from "../../auth";
import { useBuildInfo, useDiagnosticsExport } from "../../system";
import { Button, FailureNotice, QueryContent, Skeleton, useTooltip, VisuallyHidden } from "../../../shared/ui";
import { useAbout } from "../hooks/use-insights";
import { projectLinks, type BuildLine } from "./project-links";
import styles from "./about-settings.module.css";

const ICON = 18;

/**
 * 设置 › 关于（所有人）：应用标志与名称、一行图标链接（源代码、更新说明、许可、第三方许可、反馈问题）。
 * 管理员另外看到版本与 commit，并可以复制诊断信息附进反馈。文字都在悬停提示与读屏名里，页面上只有名称与版本。
 */
export function AboutSettings({ mark }: { mark: ReactNode }) {
  const about = useAbout();
  const { viewer } = useViewerScope();
  return (
    <QueryContent query={about} loading={<Skeleton label="正在加载" lines={2} />}>
      {(data) =>
        viewer.role === "admin" ? <AdminAbout mark={mark} sourceUrl={data.source_url} /> : <AboutCard mark={mark} sourceUrl={data.source_url} build={null} />
      }
    </QueryContent>
  );
}

function AdminAbout({ mark, sourceUrl }: { mark: ReactNode; sourceUrl: string }) {
  const build = useBuildInfo();
  const diagnostics = useDiagnosticsExport();
  return (
    <>
      <AboutCard mark={mark} sourceUrl={sourceUrl} build={build.data ?? null} buildPending={build.isPending} diagnostics={diagnostics} />
      {diagnostics.error === null ? null : <FailureNotice error={diagnostics.error} title="没有拿到诊断信息" />}
    </>
  );
}

type AboutCardProps = {
  mark: ReactNode;
  sourceUrl: string;
  /** null：看不到版本（成员），或管理员读取版本失败。 */
  build: BuildLine | null;
  buildPending?: boolean;
  diagnostics?: ReturnType<typeof useDiagnosticsExport>;
};

function AboutCard({ mark, sourceUrl, build, buildPending = false, diagnostics }: AboutCardProps) {
  const nameId = useId();
  const links = projectLinks(sourceUrl, build);
  const github = links.releaseNotes !== null;
  return (
    <section className={styles.card} aria-labelledby={nameId}>
      <div className={styles.identity}>
        <span className={styles.plate} aria-hidden="true">
          {mark}
        </span>
        <div className={styles.titles}>
          <h2 id={nameId} className={styles.name}>
            ROOMusic
          </h2>
          {build !== null ? <Build build={build} commitUrl={links.commit} /> : buildPending ? <span className={styles.buildPlaceholder} /> : null}
        </div>
      </div>
      <div className={styles.links}>
        <ul className={styles.group} aria-label="项目">
          <li>
            <IconLink href={links.source} label="源代码" tooltip={`源代码 ${links.sourceLabel}`}>
              {github ? <GitHubMark /> : <Code size={ICON} aria-hidden="true" />}
            </IconLink>
          </li>
          {links.releaseNotes === null ? null : (
            <li>
              <IconLink href={links.releaseNotes} label={build?.version ? `${build.version} 的更新说明` : "更新说明"}>
                <ScrollText size={ICON} aria-hidden="true" />
              </IconLink>
            </li>
          )}
          <li>
            <IconLink href={links.license} label="许可 AGPL-3.0">
              <Scale size={ICON} aria-hidden="true" />
            </IconLink>
          </li>
          {links.thirdParty === null ? null : (
            <li>
              <IconLink href={links.thirdParty} label="第三方许可">
                <Blocks size={ICON} aria-hidden="true" />
              </IconLink>
            </li>
          )}
        </ul>
        {links.feedback === null && diagnostics === undefined ? null : (
          <ul className={styles.group} aria-label="反馈">
            {links.feedback === null ? null : (
              <li>
                <IconLink href={links.feedback} label="反馈问题">
                  <MessageSquareWarning size={ICON} aria-hidden="true" />
                </IconLink>
              </li>
            )}
            {diagnostics === undefined ? null : (
              <li>
                <DiagnosticsButton diagnostics={diagnostics} />
              </li>
            )}
          </ul>
        )}
      </div>
    </section>
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

function DiagnosticsButton({ diagnostics }: { diagnostics: ReturnType<typeof useDiagnosticsExport> }) {
  const verbOf = (delivery: "copy" | "download") => (delivery === "copy" ? "复制" : "下载");
  const verb = verbOf(diagnostics.delivery);
  const label = diagnostics.done !== null ? `已${verbOf(diagnostics.done)}诊断信息` : diagnostics.pending ? `正在${verb}诊断信息` : `${verb}诊断信息`;
  return (
    <>
      <Button
        variant="icon"
        label={label}
        tooltip={diagnostics.done !== null ? label : `${label}，附进反馈`}
        busy={diagnostics.pending}
        icon={diagnostics.done !== null ? <Check size={ICON} aria-hidden="true" /> : <Stethoscope size={ICON} aria-hidden="true" />}
        onClick={diagnostics.run}
      />
      <span className={styles.liveRegion} role="status">
        {diagnostics.done !== null ? label : ""}
      </span>
    </>
  );
}

function IconLink({ href, label, tooltip, children }: { href: string; label: string; tooltip?: string; children: ReactNode }) {
  const tip = useTooltip(tooltip ?? label);
  return (
    <>
      <a className={styles.iconLink} href={href} target="_blank" rel="noreferrer" aria-label={label} {...tip.triggerProps}>
        {children}
      </a>
      {tip.tooltip}
    </>
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
