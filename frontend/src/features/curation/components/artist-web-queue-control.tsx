import { CircleStop, UserRoundSearch } from "lucide-react";
import { formatCount } from "../../../shared/lib/number";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, FailureNotice } from "../../../shared/ui";
import { useArtistWebQueue, useArtistWebQueueCommand } from "../hooks/use-artist-web-queue";
import styles from "./artist-web-queue-control.module.css";

/**
 * 艺人列表页标题旁的“整理艺人”（管理员，ADR 0009 第 7 节）：一键把所有没对上 MusicBrainz 的艺人排进去联网查找，专辑艺人在前。
 * 只显示图标，说明在悬停提示与读屏名里。在查时旁边写出已处理几位、共几位，并换成停止按钮；额度用完时写出何时继续。
 * 整理用的模型与管家的网页搜索没配好时整个控件不显示。
 */
export function ArtistWebQueueControl() {
  const queue = useArtistWebQueue();
  const command = useArtistWebQueueCommand();
  const data = queue.data;
  if (data === undefined || !data.configured) return null;
  const running = data.state === "running";
  const progress = data.total === 0 ? null : `${formatCount(data.processed)}/${formatCount(data.total)}`;
  const status = running
    ? data.retry_at === null
      ? progress
      : `额度用完，${formatDateTime(data.retry_at)} 继续`
    : data.state === "stopped" && progress !== null
      ? `已停在 ${progress}`
      : null;
  return (
    <span className={styles.control}>
      {status === null ? null : (
        <span className={styles.status} role="status">
          {status}
        </span>
      )}
      {running ? (
        <Button
          variant="icon"
          icon={<CircleStop size={16} />}
          label="停止整理艺人"
          tooltip="停止整理艺人：没查的保留，之后可以接着查"
          disabled={!data.can_stop || command.isPending}
          onClick={() => command.mutate("stop")}
        />
      ) : (
        <Button
          variant="icon"
          icon={<UserRoundSearch size={16} />}
          label={data.state === "stopped" ? "接着整理艺人" : "整理艺人"}
          tooltip="整理艺人：把没对上 MusicBrainz 的艺人逐个联网查找，专辑艺人在前"
          disabled={!data.can_start || command.isPending}
          onClick={() => command.mutate("start")}
        />
      )}
      {command.isError ? <FailureNotice error={command.error} context="write" /> : null}
    </span>
  );
}
