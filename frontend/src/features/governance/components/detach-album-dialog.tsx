import { ConfirmDialog, FailureNotice } from "../../../shared/ui";
import type { ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { governanceFailureMessages } from "./governance-messages";

type DetachAlbumDialogProps = {
  /** 要移出的版本；null 表示对话框关闭。revision 是页面读到的人工修改版本号，提交时作为预期版本。 */
  release: (ReleaseTarget & { revision: number }) | null;
  commands: ReleaseCommands;
  onClose: () => void;
};

/**
 * 把这个版本移出为独立专辑前确认，并说明结果：它成为单独的一张专辑，原来的专辑和其他版本不受影响，之后可以撤销。
 * 结果不确定时对话框保持打开，“确认”变成用同一个幂等键“重试移出”；成功或明确的拒绝（例如 409）关闭对话框，由页面上的提示说明结果。
 */
export function DetachAlbumDialog({ release, commands, onClose }: DetachAlbumDialogProps) {
  const { state } = commands;
  const pending = state.status === "pending" && state.command.kind === "detach";
  const uncertainFailure = state.status === "failed" && state.command.kind === "detach" && state.uncertain ? state : null;

  // 成功之后页面已经带到新的专辑（命令 hook 的 onMoved），这里只关闭对话框。
  const callbacks = {
    onSucceeded: onClose,
    onFailed: (_failure: unknown, uncertain: boolean) => {
      if (!uncertain) onClose();
    },
  };

  return (
    <ConfirmDialog
      open={release !== null}
      title="把这个版本移出为独立专辑？"
      confirmLabel={uncertainFailure === null ? "移出" : "重试移出"}
      pending={pending}
      error={
        uncertainFailure === null ? undefined : (
          <FailureNotice error={uncertainFailure.error} context="write" messages={governanceFailureMessages} title="结果未确认" />
        )
      }
      onConfirm={() => {
        if (release === null) return;
        if (uncertainFailure === null) commands.submit({ kind: "detach", release, revision: release.revision }, callbacks);
        else commands.retry(callbacks);
      }}
      onCancel={() => {
        commands.dismiss();
        onClose();
      }}
    >
      <p>《{release?.title ?? ""}》会从现在的专辑里分出来，成为单独的一张专辑；这张专辑里的其他版本不受影响。</p>
      <p>不会改动音乐文件。之后可以撤销，也可以再把它归入别的专辑。</p>
    </ConfirmDialog>
  );
}
