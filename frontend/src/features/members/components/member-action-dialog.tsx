import { ConfirmDialog, Notice } from "../../../shared/ui";
import type { MemberDTO } from "../api/decoders";

export type MemberAction = { kind: "disable" | "revoke"; member: MemberDTO };

type MemberActionDialogProps = {
  /** 要确认的操作；null 表示对话框关闭。 */
  action: MemberAction | null;
  /** 目标是当前登录的账号：确认后会话会被撤销，用户会被退出登录。 */
  isSelf: boolean;
  pending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
};

/**
 * 停用成员与撤销会话的确认：说明影响范围（立即退出登录、会话全部撤销），目标是自己时额外提示会被退出登录。
 * 停用最后一个管理员会被服务器拒绝（409 last_admin），对话框里先说明这一点。
 */
export function MemberActionDialog({ action, isSelf, pending, onConfirm, onCancel }: MemberActionDialogProps) {
  const name = action?.member.username ?? "";
  const disabling = action?.kind === "disable";
  return (
    <ConfirmDialog
      open={action !== null}
      title={disabling ? `停用成员“${name}”？` : `撤销成员“${name}”的全部会话？`}
      confirmLabel={disabling ? "停用成员" : "撤销会话"}
      pending={pending}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      {isSelf ? <Notice tone="warning">这是你自己的账号，确认后你会马上退出登录。</Notice> : null}
      {disabling ? (
        <p>停用后对方会马上退出登录，之后可以重新启用。</p>
      ) : (
        <p>对方的所有设备都会退出登录，需要重新登录。</p>
      )}
    </ConfirmDialog>
  );
}
