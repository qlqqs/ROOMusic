import { CircleCheck, CirclePause, KeyRound, LogOut, MoreHorizontal, UserCheck, UserX } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { DataTable, MenuButton, StatusTag, VisuallyHidden, type Column, type MenuAction } from "../../../shared/ui";
import { roleLabels } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import styles from "./member-table.module.css";

type MemberTableProps = {
  members: readonly MemberDTO[];
  /** 当前登录的用户 ID，用来标出“你自己”。 */
  viewerId: string;
  /** 有命令在进行中：菜单里的操作都停用，同一时刻只有一个成员命令。 */
  busy: boolean;
  onDisable: (member: MemberDTO) => void;
  onEnable: (member: MemberDTO) => void;
  onRevoke: (member: MemberDTO) => void;
  /** 其他成员（启用或停用）的行才有“重置密码”；自己改密码走侧栏的“修改密码”。 */
  onResetPassword: (member: MemberDTO) => void;
};

/**
 * 成员列表：用户名、角色、状态、创建时间，行尾“更多”菜单里是可用的操作（带图标，与目录行一致）。
 * 停用是破坏性操作，单独放在最后一组、用警示色；停用与撤销会话要确认，重新启用直接执行，重置密码在对话框里输入新密码。
 */
export function MemberTable({ members, viewerId, busy, onDisable, onEnable, onRevoke, onResetPassword }: MemberTableProps) {
  const actionsOf = (member: MemberDTO): MenuAction[] => {
    const actions: MenuAction[] = [];
    if (member.id !== viewerId) {
      actions.push({ id: "reset", label: "重置密码…", icon: <KeyRound size={16} />, disabled: busy, onSelect: () => onResetPassword(member) });
    }
    if (member.disabled) {
      actions.push({ id: "enable", label: "重新启用", icon: <UserCheck size={16} />, disabled: busy, onSelect: () => onEnable(member) });
      return actions;
    }
    actions.push(
      { id: "revoke", label: "撤销会话…", icon: <LogOut size={16} />, disabled: busy, onSelect: () => onRevoke(member) },
      { id: "disable", label: "停用…", icon: <UserX size={16} />, disabled: busy, danger: true, onSelect: () => onDisable(member) },
    );
    return actions;
  };
  const columns: Column<MemberDTO>[] = [
    {
      id: "username",
      header: "用户名",
      rowHeader: true,
      cell: (member) => (
        <span className={styles.name}>
          <span>{member.username}</span>
          {member.id === viewerId ? <StatusTag tone="outline">你</StatusTag> : null}
        </span>
      ),
    },
    { id: "role", header: "角色", cell: (member) => <StatusTag>{roleLabels[member.role]}</StatusTag> },
    {
      id: "status",
      header: "状态",
      cell: (member) =>
        member.disabled ? (
          <StatusTag tone="outline" icon={<CirclePause size={14} />}>
            已停用
          </StatusTag>
        ) : (
          <StatusTag tone="neutral" icon={<CircleCheck size={14} />}>
            启用
          </StatusTag>
        ),
    },
    { id: "created", header: "创建时间", cell: (member) => formatDateTime(member.created_at) },
    {
      id: "actions",
      header: "操作",
      headerContent: <VisuallyHidden>操作</VisuallyHidden>,
      hideLabelWhenStacked: true,
      besideRowHeader: true,
      cell: (member) => <MenuButton iconOnly icon={<MoreHorizontal size={18} />} label={`成员 ${member.username} 的更多操作`} actions={actionsOf(member)} />,
    },
  ];
  return (
    <div className={styles.table}>
      <DataTable caption="成员列表" columns={columns} rows={members} rowKey={(member) => member.id} />
    </div>
  );
}
