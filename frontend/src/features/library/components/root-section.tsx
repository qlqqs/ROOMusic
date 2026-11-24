import { Plus } from "lucide-react";
import { useState } from "react";
import { Button, QueryContent, SettingsGroup, Skeleton } from "../../../shared/ui";
import type { RootDTO } from "../api/decoders";
import { useRootCommands, type RootTarget } from "../hooks/use-root-commands";
import { useRoots } from "../hooks/use-roots";
import { useBeginScan } from "../hooks/use-scan-commands";
import { AddRootDialog } from "./add-root-dialog";
import { DisableRootDialog } from "./disable-root-dialog";
import { RootCommandNotice } from "./root-command-notice";
import { RootExclusionsDialog } from "./root-exclusions-dialog";
import { RelocateRootDialog } from "./relocate-root-dialog";
import { RootTable } from "./root-table";
import { StartScanFeedback } from "./start-scan-feedback";

const toTarget = (root: RootDTO): RootTarget => ({ id: root.id, name: root.name, revision: root.revision });

/**
 * 音乐目录页的“目录”卡片：标题行的“添加目录…”打开对话框；每个目录一行，停用（确认）、恢复、改路径与不扫描的文件夹在行尾“更多”里。
 * 改完路径自动开始扫描。命令结果显示在卡片下方（有对话框开着时显示在对话框里）。
 */
export function RootSection() {
  const roots = useRoots();
  const commands = useRootCommands();
  const [confirming, setConfirming] = useState<RootTarget | null>(null);
  // 只记目录 ID：对话框里显示的排除项取自列表缓存，增删后跟着刷新。
  const [editingId, setEditingId] = useState<string | null>(null);
  const editing = roots.data?.find((root) => root.id === editingId) ?? null;
  const [relocatingId, setRelocatingId] = useState<string | null>(null);
  const relocating = roots.data?.find((root) => root.id === relocatingId) ?? null;
  const scan = useBeginScan();
  const [adding, setAdding] = useState(false);
  const [path, setPath] = useState("");
  const { state } = commands;

  // 重试成功后和首次提交成功一样清空输入并关闭添加对话框；用户在失败后改了输入，就不动它。
  const retry = () =>
    commands.retry({
      onSucceeded: () => {
        if (state.status === "failed" && state.command.kind === "create" && state.command.path === path.trim()) {
          setPath("");
          setAdding(false);
        }
      },
    });

  return (
    // 页面标题已经是“音乐目录”，这一组只叫“目录”，不重复同一个词。
    <SettingsGroup
      title="目录"
      action={
        // 还没有目录时这是第一步，用主要按钮的样子（此时“开始扫描”不可用，页面上仍只有一个主要操作）。
        <Button
          variant={roots.data?.length === 0 ? "primary" : "secondary"}
          icon={<Plus size={16} />}
          onClick={() => {
            commands.dismiss();
            setAdding(true);
          }}
        >
          添加目录…
        </Button>
      }
      footer={
        <>
          {/* 对话框打开时，结果与错误显示在对话框里，页面上不重复。 */}
          {!adding && confirming === null && editing === null && relocating === null ? (
            <RootCommandNotice state={state} onRetry={retry} onDismiss={commands.dismiss} onStartScan={() => scan.begin()} scanPending={scan.start.isPending} />
          ) : null}
          <StartScanFeedback start={scan.start} begin={scan.begin} />
        </>
      }
    >
      <QueryContent query={roots} loading={<Skeleton label="正在加载目录" lines={3} />}>
        {(items) =>
          items.length === 0 ? (
            <p>还没有音乐目录。添加放音乐的目录之后再扫描，专辑就会出现在资料库里。</p>
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
              onRelocate={(root) => {
                commands.dismiss();
                scan.start.reset();
                setRelocatingId(root.id);
              }}
            />
          )
        }
      </QueryContent>
      <AddRootDialog open={adding} commands={commands} path={path} onPathChange={setPath} onRetry={retry} onClose={() => setAdding(false)} />
      <DisableRootDialog root={confirming} commands={commands} onClose={() => setConfirming(null)} />
      <RootExclusionsDialog root={editing} commands={commands} onClose={() => setEditingId(null)} />
      <RelocateRootDialog
        root={relocating}
        commands={commands}
        onClose={() => setRelocatingId(null)}
        onRelocated={() => {
          setRelocatingId(null);
          scan.begin();
        }}
      />
    </SettingsGroup>
  );
}
