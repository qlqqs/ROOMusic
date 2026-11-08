import { failureOf } from "../../../shared/api";
import { Button, Notice, QueryContent, Skeleton, VisuallyHidden } from "../../../shared/ui";
import type { ScanScheduleDTO } from "../api/decoders";
import { useSaveScanSchedule, useScanSchedule } from "../hooks/use-scan-schedule";
import styles from "./scan-schedule-control.module.css";

const hours = Array.from({ length: 24 }, (_, hour) => hour);

/** 扫描区段里的“每天自动扫描”：开关，开着时旁边选整点；改动立即保存。 */
export function ScanScheduleControl() {
  const schedule = useScanSchedule();
  return (
    <QueryContent query={schedule} loading={<Skeleton label="正在加载自动扫描设置" lines={1} />}>
      {(data) => <ScheduleRow saved={data} />}
    </QueryContent>
  );
}

/** 保存中显示正在保存的值；失败时回到服务器上的值，提示一句并可用同样的值重试。 */
function ScheduleRow({ saved }: { saved: ScanScheduleDTO }) {
  const save = useSaveScanSchedule();
  const shown = save.isPending ? save.variables : saved;
  const change = (next: Partial<ScanScheduleDTO>) => save.mutate({ ...shown, ...next });
  const failure = save.isError ? failureOf(save.error) : null;
  const requestId = failure === null || failure.kind === "aborted" || failure.requestId === "" ? null : failure.requestId;

  return (
    <div className={styles.schedule}>
      <div className={styles.row}>
        <label className={styles.switchRow}>
          <span>每天自动扫描</span>
          <input type="checkbox" role="switch" className={styles.switch} checked={shown.enabled} onChange={(event) => change({ enabled: event.target.checked })} />
        </label>
        {shown.enabled ? (
          <label className={styles.hour}>
            <VisuallyHidden>自动扫描时间</VisuallyHidden>
            <select value={shown.hour} onChange={(event) => change({ hour: Number(event.target.value) })}>
              {hours.map((hour) => (
                <option key={hour} value={hour}>
                  {hour}:00
                </option>
              ))}
            </select>
          </label>
        ) : null}
      </div>
      {save.isError ? (
        <Notice tone="error" requestId={requestId} action={<Button onClick={() => save.mutate(save.variables)}>重试</Button>}>
          没保存上。
        </Notice>
      ) : null}
    </div>
  );
}
