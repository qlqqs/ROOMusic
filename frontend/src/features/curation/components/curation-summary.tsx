import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { Sparkles } from "lucide-react";
import { useState } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Button, Notice, PendingStatusRow, StatusRow } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { programStateText } from "../curation-text";
import { dismissHint, hintDismissed, shouldShowFirstScanHint } from "../first-scan-hint";
import { useCurationProgress, useCurationSettings } from "../hooks/use-curation";
import { processedRatio } from "./curation-progress-section";
import styles from "./curation-sections.module.css";

const icon = <Sparkles size={16} />;

/**
 * 概览“资料库状态”里的自动整理一行（管理员）：未打开写“未打开”；打开后写状态与“已处理 N / M 个发行”，右侧是百分比；整行链接到设置。
 */
export function CurationSummary() {
  const settings = useCurationSettings();
  const progress = useCurationProgress(settings.data);
  const link = ({ className, children }: { className: string; children: ReactNode }) => (
    <Link to="/settings/curation" className={className}>
      {children}
    </Link>
  );
  if (settings.data === undefined) return <PendingStatusRow icon={icon} label="自动整理" query={settings} />;
  if (!settings.data.enabled) return <StatusRow icon={icon} label="自动整理" value="未打开" renderLink={link} />;
  if (progress.data === undefined) return <PendingStatusRow icon={icon} label="自动整理" query={progress} />;
  const data = progress.data;
  return (
    <StatusRow
      icon={icon}
      label="自动整理"
      detail={`${programStateText[data.state]}，已处理 ${formatCount(data.releases.processed)} / ${formatCount(data.releases.total)} 个发行`}
      value={`${Math.floor(processedRatio(data) * 100)}%`}
      renderLink={link}
    />
  );
}

/**
 * 概览顶部的一次性提示（管理员）：曲库里已有发行而自动整理从未打开过时，建议打开；“不再提示”记在这个浏览器上。
 */
export function FirstScanHint() {
  const { viewer } = useViewerScope();
  // 成员不读管理员接口：只在管理员时挂载读取设置与进度的部分。
  return viewer.role === "admin" ? <AdminFirstScanHint userId={viewer.id} /> : null;
}

function AdminFirstScanHint({ userId }: { userId: string }) {
  const [dismissed, setDismissed] = useState(() => hintDismissed(userId));
  const settings = useCurationSettings();
  const progress = useCurationProgress(settings.data);
  if (!shouldShowFirstScanHint({ isAdmin: true, settings: settings.data, progress: progress.data, dismissed })) return null;
  return (
    <Notice
      tone="info"
      title="曲库扫描好了"
      action={
        <span className={styles.footerRow}>
          <Link to="/settings/curation">前往设置</Link>
          <Button
            onClick={() => {
              dismissHint(userId);
              setDismissed(true);
            }}
          >
            不再提示
          </Button>
        </span>
      }
    >
      打开自动整理，可以自动补全封面、歌词与空着的信息；每一项都能撤销。
    </Notice>
  );
}
