import { Link } from "@tanstack/react-router";
import { Combine, History, PencilLine, Split } from "lucide-react";
import { useState } from "react";
import { Button } from "../../../shared/ui";
import type { ArtistCommands } from "../hooks/use-artist-commands";
import { ArtistCommandNotice } from "./artist-command-notice";
import { MergeArtistDialog } from "./merge-artist-dialog";
import { RenameArtistDialog } from "./rename-artist-dialog";
import { SplitArtistDialog } from "./split-artist-dialog";
import styles from "./release-admin-actions.module.css";

type ArtistAdminActionsProps = {
  /** 页面读到的艺人：revision 是人工修改版本号（合并、拆开、改名的预期版本），nameSource 是主名来自最常见写法还是管理员。 */
  artist: { id: string; name: string; revision: number; nameSource: "auto" | "manual" };
  commands: ArtistCommands;
};

/**
 * 艺人页里管理员的操作区（只有管理员看到；后端仍独立鉴权）：合并艺人…、拆开…、修改主名…，这位艺人的操作记录，以及上一次操作的结果。
 * 这些操作不能撤销（反向操作就是恢复的办法），确认前各用一句话说明。命令的状态由页面持有。
 */
export function ArtistAdminActions({ artist, commands }: ArtistAdminActionsProps) {
  const [dialog, setDialog] = useState<"merge" | "split" | "rename" | null>(null);
  const open = (next: "merge" | "split" | "rename") => {
    commands.dismiss();
    setDialog(next);
  };
  const close = () => setDialog(null);
  return (
    <div className={styles.admin}>
      <div className={styles.row}>
        <Button icon={<Combine size={16} />} onClick={() => open("merge")}>
          合并艺人…
        </Button>
        <Button icon={<Split size={16} />} onClick={() => open("split")}>
          拆开…
        </Button>
        <Button icon={<PencilLine size={16} />} onClick={() => open("rename")}>
          修改主名…
        </Button>
        <Link to="/operations" search={{ artist: artist.id }} className={styles.link}>
          <History size={16} aria-hidden="true" /> 操作记录
        </Link>
      </div>
      <ArtistCommandNotice
        state={commands.state}
        artistId={artist.id}
        hideFailure={dialog !== null}
        onRetry={() => commands.retry()}
        onDismiss={commands.dismiss}
      />
      {dialog === "merge" ? <MergeArtistDialog open target={artist} commands={commands} onClose={close} /> : null}
      {dialog === "split" ? <SplitArtistDialog open artist={artist} commands={commands} onClose={close} /> : null}
      {dialog === "rename" ? <RenameArtistDialog open artist={artist} commands={commands} onClose={close} /> : null}
    </div>
  );
}
