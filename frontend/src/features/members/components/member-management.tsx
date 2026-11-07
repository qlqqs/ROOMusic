import { UserPlus } from "lucide-react";
import { useState } from "react";
import { Button, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import { useMemberCommands } from "../hooks/use-member-commands";
import { useMembers } from "../hooks/use-members";
import { CreateMemberDialog } from "./create-member-form";
import { MemberActionDialog, type MemberAction } from "./member-action-dialog";
import { MemberCommandNotice } from "./member-command-notice";
import { MemberTable } from "./member-table";
import { ResetPasswordDialog } from "./reset-password-dialog";

const toTarget = (member: MemberDTO) => ({ id: member.id, username: member.username });

/**
 * 成员管理页的内容：新建成员（对话框）与成员列表共用一个命令状态（同一时刻只有一个成员命令）。
 * 停用与撤销会话先确认，重置密码在对话框里输入新密码；对话框在命令结束（成功或失败）时关闭，结果显示在页面上，
 * 因为成员写操作没有幂等保证，结果不确定时不提供重试，只刷新列表让用户核对。
 */
export function MemberManagement() {
  const { viewer } = useViewerScope();
  const members = useMembers();
  const commands = useMemberCommands();
  const [confirming, setConfirming] = useState<MemberAction | null>(null);
  const [resetting, setResetting] = useState<MemberDTO | null>(null);
  const [creating, setCreating] = useState(false);
  const { state } = commands;

  const ask = (kind: MemberAction["kind"], member: MemberDTO) => {
    commands.dismiss();
    setConfirming({ kind, member });
  };
  const close = () => setConfirming(null);

  return (
    <>
      <Panel
        title="成员列表"
        action={
          <Button
            variant="primary"
            icon={<UserPlus size={16} />}
            onClick={() => {
              commands.dismiss();
              setCreating(true);
            }}
          >
            新建成员
          </Button>
        }
      >
        <MemberCommandNotice state={state} scope="list" onDismiss={commands.dismiss} />
        <QueryContent query={members} loading={<Skeleton label="正在加载成员" lines={3} />}>
          {(items) => (
            <MemberTable
              members={items}
              viewerId={viewer.id}
              busy={state.status === "pending"}
              onDisable={(member) => ask("disable", member)}
              onEnable={(member) => commands.submit({ kind: "enable", member: toTarget(member) })}
              onRevoke={(member) => ask("revoke", member)}
              onResetPassword={(member) => {
                commands.dismiss();
                setResetting(member);
              }}
            />
          )}
        </QueryContent>
      </Panel>
      <CreateMemberDialog open={creating} commands={commands} onClose={() => setCreating(false)} />
      <MemberActionDialog
        action={confirming}
        isSelf={confirming?.member.id === viewer.id}
        pending={state.status === "pending"}
        onConfirm={() => {
          if (confirming === null) return;
          commands.submit({ kind: confirming.kind, member: toTarget(confirming.member) }, { onSucceeded: close, onFailed: close });
        }}
        onCancel={() => {
          commands.dismiss();
          close();
        }}
      />
      <ResetPasswordDialog
        member={resetting}
        pending={state.status === "pending"}
        onSubmit={(password) => {
          if (resetting === null) return;
          const done = () => setResetting(null);
          commands.submit({ kind: "reset-password", member: { ...toTarget(resetting), disabled: resetting.disabled }, password }, { onSucceeded: done, onFailed: done });
        }}
        onCancel={() => {
          commands.dismiss();
          setResetting(null);
        }}
      />
    </>
  );
}
