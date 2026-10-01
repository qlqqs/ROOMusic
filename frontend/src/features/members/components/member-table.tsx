import { CircleCheck, CirclePause } from "lucide-react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, DataTable, StatusTag, type Column } from "../../../shared/ui";
import { roleLabels } from "../../auth";
import type { MemberDTO } from "../api/decoders";
import styles from "./member-table.module.css";

type MemberTableProps = {
  members: readonly MemberDTO[];
  /** 当前登录的用户名：后端没有给出用户 ID，用户名是唯一标识，用来标出“你自己”。 */
  viewerUsername: string;
  /** 有命令在进行中：所有操作按钮忙碌（点击被忽略），同一时刻只有一个成员命令。 */
  busy: boolean;
  onDisable: (member: MemberDTO) => void;
  onEnable: (member: MemberDTO) => void;
  onRevoke: (member: MemberDTO) => void;
};

/** 成员列表：用户名、角色、状态、创建时间与可用操作。停用与撤销会话要确认，重新启用直接执行。 */
export function MemberTable({ members, viewerUsername, busy, onDisable, onEnable, onRevoke }: MemberTableProps) {
  const columns: Column<MemberDTO>[] = [
    {
      id: "username",
      header: "用户名",
      rowHeader: true,
      cell: (member) => (
        <span className={styles.name}>
          <span>{member.username}</span>
          {member.username === viewerUsername ? <StatusTag tone="outline">你</StatusTag> : null}
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
      hideLabelWhenStacked: true,
      cell: (member) => (
        <span className={styles.actions}>
          {member.disabled ? (
            <Button busy={busy} aria-label={`重新启用成员 ${member.username}`} onClick={() => onEnable(member)}>
              重新启用
            </Button>
          ) : (
            <>
              <Button busy={busy} aria-label={`停用成员 ${member.username}`} onClick={() => onDisable(member)}>
                停用
              </Button>
              <Button busy={busy} aria-label={`撤销会话 ${member.username}`} onClick={() => onRevoke(member)}>
                撤销会话
              </Button>
            </>
          )}
        </span>
      ),
    },
  ];
  return <DataTable caption="成员列表" columns={columns} rows={members} rowKey={(member) => member.id} />;
}
