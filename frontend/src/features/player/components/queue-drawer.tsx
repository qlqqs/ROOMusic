import { ListMusic } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";
import { cx } from "../../../shared/lib/class-names";
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

const CLOSE_MS = 200;

/**
 * 队列抽屉：宽屏是右侧浮起的面板，窄屏是从底部升起的整屏面板。Esc、关闭图标或点外面关闭；
 * 关闭时先播放 200ms 的退场动画再真正关闭（减少动态效果时直接关闭）。
 */
function QueueDrawer({ open, onClose, renderQueueActions }: { open: boolean; onClose: () => void } & QueueButtonProps) {
  const headingId = useId();
  const [closing, setClosing] = useState(false);
  const requestClose = () => {
    if (closing) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) onClose();
    else setClosing(true);
  };
  useEffect(() => {
    if (!closing) return;
    const timer = window.setTimeout(() => {
      setClosing(false);
      onClose();
    }, CLOSE_MS);
    return () => window.clearTimeout(timer);
  }, [closing, onClose]);
  return <ModalDialog open={open} titleId={headingId} onRequestClose={requestClose} closeOnBackdrop className={cx(styles.drawer, closing && styles.closing)}>
    <div className={styles.content}><PlayerQueue headingId={headingId} onClose={requestClose} renderQueueActions={renderQueueActions} /></div>
  </ModalDialog>;
}
