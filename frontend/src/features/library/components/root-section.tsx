import { useState } from "react";
import { Panel, QueryContent, Skeleton } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import { useRootCommands, type RootTarget } from "../hooks/use-root-commands";
import { useRoots } from "../hooks/use-roots";
import { AddRootForm } from "./add-root-form";
import { DisableRootDialog } from "./disable-root-dialog";
import { RootCommandNotice } from "./root-command-notice";
import { RootTable } from "./root-table";

const toTarget = (root: RootDTO): RootTarget => ({ id: root.id, name: root.name, revision: root.revision });

/** 扫描目录页的“音乐目录”区段：新增、列表、停用（确认）与恢复。 */
export function RootSection() {
  const roots = useRoots();
  const commands = useRootCommands();
  const [confirming, setConfirming] = useState<RootTarget | null>(null);
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
      {/* 确认对话框打开时，结果与错误显示在对话框里，页面上不重复。 */}
      {confirming === null ? <RootCommandNotice state={state} onRetry={retry} onDismiss={commands.dismiss} /> : null}
      <QueryContent query={roots} loading={<Skeleton label="正在加载目录" lines={3} />}>
        {(items) =>
          items.length === 0 ? (
            <p>还没有登记音乐目录。在上方输入服务器上的绝对路径，添加第一个目录。</p>
          ) : (
            <RootTable
              roots={items}
              busy={state.status === "pending"}
              onDisable={(root) => {
                commands.dismiss();
                setConfirming(toTarget(root));
              }}
              onRestore={(root) => commands.submit({ kind: "restore", root: toTarget(root) })}
            />
          )
        }
      </QueryContent>
      <DisableRootDialog root={confirming} commands={commands} onClose={() => setConfirming(null)} />
    </Panel>
  );
}
