import { useNavigate } from "@tanstack/react-router";
import { Combine, History, PencilLine, Split } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { MenuAction } from "../../../shared/ui";
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

/** “更多”菜单里“管理”分组的组名（与专辑页相同）。 */
const ADMIN_GROUP = "管理";

/**
 * 艺人页头部里管理员的操作（只有管理员看到；后端仍独立鉴权），与专辑页的 useReleaseAdminActions 同一种组合方式。返回两部分，由页面组合：
 * - actions：放进“更多”菜单“管理”分组的项：合并艺人…、拆开…、修改主名…、操作记录（这位艺人的记录）；
 * - panel：放在操作行下方的上一次操作的结果提示与各个对话框。
 * 这些操作不能撤销（反向操作就是恢复的办法），确认前各用一句话说明。命令的状态由页面持有。
 * 菜单项打开对话框时，菜单先把焦点还给“更多”按钮，对话框关闭后焦点回到那里。
 */
export function useArtistAdminActions({ artist, commands }: ArtistAdminActionsProps): { actions: MenuAction[]; panel: ReactNode } {
  const [dialog, setDialog] = useState<"merge" | "split" | "rename" | null>(null);
  const navigate = useNavigate();
  const open = (next: "merge" | "split" | "rename") => {
    commands.dismiss();
    setDialog(next);
  };
  const close = () => setDialog(null);
  const actions: MenuAction[] = [
    { id: "merge", group: ADMIN_GROUP, label: "合并艺人…", icon: <Combine size={16} />, onSelect: () => open("merge") },
    { id: "split", group: ADMIN_GROUP, label: "拆开…", icon: <Split size={16} />, onSelect: () => open("split") },
    { id: "rename", group: ADMIN_GROUP, label: "修改主名…", icon: <PencilLine size={16} />, onSelect: () => open("rename") },
    // 导航类菜单项：菜单项是按钮，在 onSelect 里跳转（与用户菜单的“设置”相同）。
    { id: "operations", group: ADMIN_GROUP, label: "操作记录", icon: <History size={16} />, onSelect: () => void navigate({ to: "/settings/operations", search: { artist: artist.id } }) },
  ];
  const panel = (
    <div className={styles.admin}>
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
  return { actions, panel };
}
