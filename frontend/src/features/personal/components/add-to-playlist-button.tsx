import { ListPlus } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import { AddToPlaylistDialog } from "./add-to-playlist-dialog";
import styles from "./add-to-playlist-button.module.css";

type AddToPlaylistButtonProps = {
  /** 打开对话框时才取曲目（例如整张专辑当前版本的全部曲目），对话框拿到的是那一刻的快照。 */
  trackIds: () => readonly string[];
  subject: string;
  disabled?: boolean;
  /** 只显示图标（专辑头部的操作行）：读屏名与悬停提示仍是“添加到歌单…”。 */
  iconOnly?: boolean;
};

/** “添加到歌单…”按钮（专辑头部、队列等整批加入的入口）：结果提示留在按钮旁边。 */
export function AddToPlaylistButton({ trackIds, subject, disabled = false, iconOnly = false }: AddToPlaylistButtonProps) {
  const [snapshot, setSnapshot] = useState<readonly string[] | null>(null);
  const [notice, setNotice] = useState("");
  const open = () => {
    setNotice("");
    setSnapshot(trackIds());
  };
  return (
    <span className={styles.container}>
      {iconOnly ? (
        <Button variant="icon" icon={<ListPlus size={18} />} label="添加到歌单…" disabled={disabled} onClick={open} />
      ) : (
        <Button icon={<ListPlus size={16} />} disabled={disabled} onClick={open}>
          添加到歌单…
        </Button>
      )}
      <span role="status" className={styles.notice}>
        {notice}
      </span>
      <AddToPlaylistDialog
        open={snapshot !== null}
        trackIds={snapshot ?? []}
        subject={subject}
        onClose={() => setSnapshot(null)}
        onAdded={(message) => {
          setSnapshot(null);
          setNotice(message);
        }}
      />
    </span>
  );
}
