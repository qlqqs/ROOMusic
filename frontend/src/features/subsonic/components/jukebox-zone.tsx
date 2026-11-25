import { FailureNotice, SettingRow } from "../../../shared/ui";
import { useSetJukeboxZone } from "../hooks/use-subsonic";
import styles from "./subsonic.module.css";

/** 可选的播放区：id 是播放区 ID，label 是给人看的名字（编组带台数）。 */
export type JukeboxZoneOption = { id: string; label: string };

const AUTO = "";

/**
 * “点唱机控制”一行：支持点唱机模式的客户端遥控哪个播放区。即改即存，保存中显示刚选的值。
 * zones 为 null 表示播放设备还没读到，这时下拉不可用。已保存的播放区不在列表里时（例如它正作为成员在别的编组里）仍列出，免得显示成“自动”。
 */
export function JukeboxZoneRow({ saved, zones }: { saved: string | null; zones: readonly JukeboxZoneOption[] | null }) {
  const save = useSetJukeboxZone();
  const shown = save.isPending ? save.variables : saved;
  const options = zones ?? [];
  const missing = shown !== null && !options.some((zone) => zone.id === shown);
  return (
    <SettingRow
      label="点唱机控制"
      description="支持点唱机模式的客户端（DSub、Ultrasonic、Symfonium 等）可以遥控这个播放区，声音从它的设备出来。"
    >
      {(ids) => (
        <span className={styles.jukebox}>
          <select
            id={ids.controlId}
            className={styles.select}
            aria-describedby={ids.descriptionId}
            value={shown ?? AUTO}
            disabled={zones === null}
            onChange={(event) => save.mutate(event.target.value === AUTO ? null : event.target.value)}
          >
            <option value={AUTO}>最近播放过的播放区</option>
            {options.map((zone) => (
              <option key={zone.id} value={zone.id}>
                {zone.label}
              </option>
            ))}
            {missing ? <option value={shown}>已选的播放区</option> : null}
          </select>
          {save.isError ? <FailureNotice error={save.error} context="write" title="没有保存点唱机控制" messages={{ not_found: "这个播放区已经不存在。" }} /> : null}
        </span>
      )}
    </SettingRow>
  );
}
