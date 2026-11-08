import { useState } from "react";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import { useRootCommands, type RootTarget } from "../hooks/use-root-commands";
import { useRoots } from "../hooks/use-roots";
import { AddRootForm } from "./add-root-form";
import { DisableRootDialog } from "./disable-root-dialog";
import { RootCommandNotice } from "./root-command-notice";
import { RootExclusionsDialog } from "./root-exclusions-dialog";
import { RootTable } from "./root-table";

const toTarget = (root: RootDTO): RootTarget => ({ id: root.id, name: root.name, revision: root.revision });

/** 扫描目录页的“音乐目录”区段：新增、列表、停用（确认）、恢复与不扫描的文件夹。 */
export function RootSection() {
  const roots = useRoots();
  const commands = useRootCommands();
  const [confirming, setConfirming] = useState<RootTarget | null>(null);
  // 只记目录 ID：对话框里显示的排除项取自列表缓存，增删后跟着刷新。
  const [editingId, setEditingId] = useState<string | null>(null);
  const editing = roots.data?.find((root) => root.id === editingId) ?? null;
  const [path, setPath] = useState("");
  const { state } = commands;

  // 重试成功后和首次提交成功一样清空输入；用户在失败后改了输入，就不动它。
  const retry = () =>
    commands.retry({
      onSucceeded: () => {
        if (state.status === "failed" && state.command.kind === "create" && state.command.path === path.trim()) setPath("");
      },
    });

  return (
    <Panel title="音乐目录">
      <AddRootForm commands={commands} path={path} onPathChange={setPath} />
      {/* 对话框打开时，结果与错误显示在对话框里，页面上不重复。 */}
      {confirming === null && editing === null ? <RootCommandNotice state={state} onRetry={retry} onDismiss={commands.dismiss} /> : null}
      <QueryContent query={roots} loading={<Skeleton label="正在加载目录" lines={3} />}>
        {(items) =>
          items.length === 0 ? (
            <p>还没有音乐目录，在上方添加一个。</p>
          ) : (
            <RootTable
              roots={items}
              busy={state.status === "pending"}
              onDisable={(root) => {
                commands.dismiss();
                setConfirming(toTarget(root));
              }}
              onRestore={(root) => commands.submit({ kind: "restore", root: toTarget(root) })}
              onEditExclusions={(root) => {
                commands.dismiss();
                setEditingId(root.id);
              }}
            />
          )
        }
      </QueryContent>
      <DisableRootDialog root={confirming} commands={commands} onClose={() => setConfirming(null)} />
      <RootExclusionsDialog root={editing} commands={commands} onClose={() => setEditingId(null)} />
    </Panel>
  );
}
