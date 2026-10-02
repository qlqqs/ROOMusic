import { useState } from "react";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import { useMemberCommands } from "../hooks/use-member-commands";
import { useMembers } from "../hooks/use-members";
import { CreateMemberForm } from "./create-member-form";
import { MemberActionDialog, type MemberAction } from "./member-action-dialog";
import { MemberCommandNotice } from "./member-command-notice";
import { MemberTable } from "./member-table";

const toTarget = (member: MemberDTO) => ({ id: member.id, username: member.username });

/**
 * 成员管理页的内容：新建成员与成员列表共用一个命令状态（同一时刻只有一个成员命令）。
 * 停用与撤销会话先确认；对话框在命令结束（成功或失败）时关闭，结果显示在页面上，
 * 因为成员写操作没有幂等保证，结果不确定时不提供重试，只刷新列表让用户核对。
 */
export function MemberManagement() {
  const { viewer } = useViewerScope();
  const members = useMembers();
  const commands = useMemberCommands();
  const [confirming, setConfirming] = useState<MemberAction | null>(null);
  const { state } = commands;

  const ask = (kind: MemberAction["kind"], member: MemberDTO) => {
    commands.dismiss();
    setConfirming({ kind, member });
  };
  const close = () => setConfirming(null);

  return (
    <>
      <Panel title="新建成员">
        <p>新成员的角色是“成员”。目前不能在这里创建管理员或修改成员的角色。</p>
        <CreateMemberForm commands={commands} />
        <MemberCommandNotice state={state} scope="create" onDismiss={commands.dismiss} />
      </Panel>
      <Panel title="成员列表">
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
            />
          )}
        </QueryContent>
      </Panel>
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
    </>
  );
}
