import { ListMusic } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Button, ModalDialog } from "../../../shared/ui";
import type { PlayerTrack } from "../engine/queue";
import { usePlayerSelector } from "../player-provider";
import { PlayerQueue } from "./player-queue";
import styles from "./queue-drawer.module.css";

type QueueButtonProps = {
  /** 队列标题旁的动作（例如“存为歌单”），原样交给队列。 */
  renderQueueActions?: ((tracks: readonly PlayerTrack[]) => ReactNode) | undefined;
};

/** 队列图标：点开右侧的队列抽屉；关闭后焦点回到这里。播放栏与全屏底栏各一个。 */
export function QueueButton({ renderQueueActions }: QueueButtonProps) {
  const count = usePlayerSelector((snapshot) => snapshot.queue.entries.length);
  const [open, setOpen] = useState(false);
  return <>
    <Button variant="icon" icon={<ListMusic size={18} />} label={`队列（${count}）`} tooltip="队列" onClick={() => setOpen(true)} />
    <QueueDrawer open={open} onClose={() => setOpen(false)} renderQueueActions={renderQueueActions} />
  </>;
}

/** 右侧滑出的队列抽屉：Esc、关闭图标或点外面关闭。 */
function QueueDrawer({ open, onClose, renderQueueActions }: { open: boolean; onClose: () => void } & QueueButtonProps) {
  const headingId = useId();
  return <ModalDialog open={open} titleId={headingId} onRequestClose={onClose} closeOnBackdrop className={styles.drawer}>
    <div className={styles.content}><PlayerQueue headingId={headingId} onClose={onClose} renderQueueActions={renderQueueActions} /></div>
  </ModalDialog>;
}
