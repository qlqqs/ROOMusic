import { Check, CircleAlert, Clock3, Radio } from "lucide-react";
import { Button, useMenu, useTooltip, type MenuAction } from "../../../shared/ui";
import { pollIntervalMinutes, type RootListItemDTO } from "../api/decoders";
import { useSaveRootScanInterval } from "../hooks/use-root-scan-interval";
import styles from "./root-watch-mode.module.css";

const REALTIME = "实时监听：文件有改动，几秒内重扫那个文件夹";

/** 间隔的说法：15 → 每 15 分钟，60 → 每小时，240 → 每 4 小时，1440 → 每天。 */
export function pollIntervalText(minutes: number): string {
  if (minutes === 1440) return "每天";
  if (minutes === 60) return "每小时";
  if (minutes % 60 === 0) return `每 ${minutes / 60} 小时`;
  return `每 ${minutes} 分钟`;
}

/**
 * 目录行里的监听方式。实时监听只是一个图标，说明在悬停提示与读屏名里；定时扫描是时钟图标按钮，点开选间隔，选中立即保存。
 * 停用的目录、首次完整扫描之前（方式还没定）不显示。保存中显示正在保存的值；失败时旁边出现提示图标，点按用同样的值重试。
 */
export function RootWatchMode({ root }: { root: RootListItemDTO }) {
  const save = useSaveRootScanInterval();
  const realtimeTip = useTooltip(REALTIME);
  const shown = save.isPending ? save.variables.minutes : root.poll_interval_minutes;
  const text = pollIntervalText(shown);
  const actions: MenuAction[] = pollIntervalMinutes.map((minutes) => ({
    id: String(minutes),
    label: pollIntervalText(minutes),
    group: "定时扫描间隔",
    icon: minutes === shown ? <Check size={16} /> : <span className={styles.blank} />,
    onSelect: () => {
      if (minutes !== shown) save.mutate({ id: root.id, minutes });
    },
  }));
  const menu = useMenu({ label: "定时扫描间隔", actions });

  if (root.status !== "active" || root.scan_watch_mode === null) return null;
  if (root.scan_watch_mode === "realtime") {
    return (
      <>
        <span className={styles.realtime} role="img" aria-label={REALTIME} {...realtimeTip.triggerProps}>
          <Radio size={16} aria-hidden="true" />
        </span>
        {realtimeTip.tooltip}
      </>
    );
  }
  return (
    <span className={styles.scheduled}>
      {save.isError ? (
        <Button
          variant="icon"
          icon={<CircleAlert size={16} className={styles.failed} />}
          label="没保存上扫描间隔，点按重试"
          onClick={() => save.mutate(save.variables)}
        />
      ) : null}
      <Button
        {...menu.triggerProps}
        variant="icon"
        icon={<Clock3 size={16} />}
        label={`定时扫描，${text}`}
        tooltip={`这个目录无法实时监听，${text}扫描一次`}
      />
      {menu.menu}
    </span>
  );
}
