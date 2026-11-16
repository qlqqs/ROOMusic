import { BookmarkPlus, Plus } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { formatDateTime } from "../../../shared/lib/time";
import { Button, ConfirmDialog, EmptyState, FailureNotice, ModalDialog, Notice, QueryContent, SettingsGroup, Skeleton } from "../../../shared/ui";
import type { MemoryDTO } from "../api/decoders";
import { useClearMemories, useCreateMemory, useDeleteMemory, useMemories, useUpdateMemory } from "../hooks/use-memories";
import { DEFAULT_STEWARD_NAME } from "../hooks/use-steward-settings";
import styles from "./settings.module.css";

/** 字数按 Unicode 字符计（与后端的上限一致），首尾空白不算。 */
const characters = (text: string) => [...text.trim()].length;

/** 新增、修改的失败。上限的数字取自列表，随失败一起说明。 */
function memoryFailureMessages(limit: number, maxLength: number): Readonly<Record<string, string>> {
  return {
    invalid_input: `内容不能为空，最多 ${maxLength} 个字。`,
    memory_limit_reached: `记忆已经有 ${limit} 条了，删掉不用的再添加。`,
    not_found: "这条记忆已经不在了，可能刚被删除。",
    steward_disabled: "管理员暂时关闭了成员使用管家，现在只能查看和删除记忆。",
  };
}

const deleteFailureMessages: Readonly<Record<string, string>> = { not_found: "这条记忆已经不在了，可能刚被删除。" };

type Editing = { kind: "new" } | { kind: "edit"; memory: MemoryDTO };

/**
 * 设置页 Agent 栏目里“记忆”一组（C10）：管家在对话里按你的要求记下的、或你在这里手动添加的内容，每次对话都放进发给模型的提示里。
 * 列表按更新时间倒序，可以添加、修改、删除与全部清除（先确认）；显示条数与字数的上限（取自接口）。
 * readOnly：管理员关闭了成员使用管家，成员只能查看与删除。记忆的内容来自用户或模型，只按文字显示。
 */
export function AgentMemories({ readOnly }: { readOnly: boolean }) {
  const memories = useMemories();
  const create = useCreateMemory();
  const update = useUpdateMemory();
  const remove = useDeleteMemory();
  const clear = useClearMemories();
  const [editing, setEditing] = useState<Editing | null>(null);
  const [removing, setRemoving] = useState<MemoryDTO | null>(null);
  const [clearing, setClearing] = useState(false);
  const listId = useId();
  const data = memories.data;
  const full = data !== undefined && data.items.length >= data.limit;
  const empty = data === undefined || data.items.length === 0;

  const openEditor = (next: Editing) => {
    create.reset();
    update.reset();
    remove.reset();
    clear.reset();
    setEditing(next);
  };
  const save = (content: string) => {
    const done = () => setEditing(null);
    if (editing?.kind === "edit") update.mutate({ id: editing.memory.id, content }, { onSuccess: done });
    else create.mutate(content, { onSuccess: done });
  };
  const saving = editing?.kind === "edit" ? update : create;

  return (
    <>
      <SettingsGroup
        title="记忆"
        action={
          <div className={styles.actions}>
            <Button variant="primary" icon={<Plus size={16} />} disabled={readOnly || data === undefined || full} onClick={() => openEditor({ kind: "new" })}>
              添加记忆
            </Button>
            <Button
              disabled={empty || clear.isPending}
              onClick={() => {
                clear.reset();
                setClearing(true);
              }}
            >
              全部清除…
            </Button>
          </div>
        }
      >
        {readOnly ? <Notice tone="warning">管理员暂时关闭了成员使用{DEFAULT_STEWARD_NAME}，记忆只能查看和删除。</Notice> : null}
        {remove.isError && removing === null ? <FailureNotice error={remove.error} context="write" messages={deleteFailureMessages} title="没有删除" action={<Button onClick={remove.reset}>关闭</Button>} /> : null}
        {clear.isError && !clearing ? <FailureNotice error={clear.error} context="write" title="没有清除" action={<Button onClick={clear.reset}>关闭</Button>} /> : null}
        <QueryContent query={memories} loading={<Skeleton label="正在读取记忆" lines={3} />}>
          {(list) => (
            <>
              <p className={styles.note}>
                共 {list.items.length} 条，最多 {list.limit} 条；每条最多 {list.max_length} 个字。只有你自己能看到，每次对话都会发给你配置的模型。
              </p>
              {!readOnly && list.items.length >= list.limit ? <Notice>记忆已经到了上限（{list.limit} 条），删掉不用的才能添加。</Notice> : null}
              {list.items.length === 0 ? (
                <EmptyState icon={<BookmarkPlus size={24} />} title="还没有记忆">
                  记忆是{DEFAULT_STEWARD_NAME}在对话里按你的要求记下的，或你在这里手动添加的偏好，比如“回答都用繁体”。只有你自己能看到，每次对话都会发给你配置的模型。
                </EmptyState>
              ) : (
                <ul className={styles.memories} aria-label="记忆">
                  {list.items.map((memory) => (
                    <li key={memory.id} className={styles.memory}>
                      <p id={`${listId}-${memory.id}`} className={styles.memoryText}>
                        {memory.content}
                      </p>
                      <div className={styles.memoryMeta}>
                        <span className={styles.muted}>更新于 {formatDateTime(memory.updated_at)}</span>
                        <span className={styles.actions}>
                          {readOnly ? null : (
                            <Button aria-describedby={`${listId}-${memory.id}`} onClick={() => openEditor({ kind: "edit", memory })}>
                              修改…
                            </Button>
                          )}
                          <Button
                            aria-describedby={`${listId}-${memory.id}`}
                            disabled={remove.isPending}
                            onClick={() => {
                              remove.reset();
                              setRemoving(memory);
                            }}
                          >
                            删除…
                          </Button>
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </QueryContent>
      </SettingsGroup>
      {/* 每次打开都是新的表单。 */}
      {editing === null || data === undefined ? null : (
        <MemoryDialog
          initial={editing.kind === "edit" ? editing.memory.content : ""}
          title={editing.kind === "edit" ? "修改记忆" : "添加记忆"}
          maxLength={data.max_length}
          pending={saving.isPending}
          error={saving.isError ? saving.error : null}
          messages={memoryFailureMessages(data.limit, data.max_length)}
          onClose={() => setEditing(null)}
          onSubmit={save}
        />
      )}
      <ConfirmDialog
        open={removing !== null}
        title="删除这条记忆？"
        confirmLabel="删除"
        pending={remove.isPending}
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (removing === null) return;
          const done = () => setRemoving(null);
          remove.mutate(removing.id, { onSuccess: done, onError: done });
        }}
      >
        <p className={styles.memoryText}>{removing?.content}</p>
        <p>删除后{DEFAULT_STEWARD_NAME}不再参考它，不能恢复。</p>
      </ConfirmDialog>
      <ConfirmDialog
        open={clearing}
        title="清除全部记忆？"
        confirmLabel="全部清除"
        pending={clear.isPending}
        onCancel={() => setClearing(false)}
        onConfirm={() => {
          const done = () => setClearing(false);
          clear.mutate(undefined, { onSuccess: done, onError: done });
        }}
      >
        <p>
          {data?.items.length ?? 0} 条记忆都会删除，不能恢复。之后{DEFAULT_STEWARD_NAME}不再参考它们，需要时可以让它重新记住。
        </p>
      </ConfirmDialog>
    </>
  );
}

function MemoryDialog({
  initial,
  title,
  maxLength,
  pending,
  error,
  messages,
  onClose,
  onSubmit,
}: {
  initial: string;
  title: string;
  maxLength: number;
  pending: boolean;
  error: unknown;
  messages: Readonly<Record<string, string>>;
  onClose: () => void;
  onSubmit: (content: string) => void;
}) {
  const titleId = useId();
  const fieldId = useId();
  const counterId = useId();
  const field = useRef<HTMLTextAreaElement>(null);
  const [value, setValue] = useState(initial);
  const length = characters(value);
  const tooLong = length > maxLength;
  const unchanged = value.trim() === initial.trim();
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (length === 0 || tooLong || unchanged) return;
    onSubmit(value.trim());
  };
  const close = () => {
    if (!pending) onClose();
  };
  return (
    <ModalDialog open titleId={titleId} initialFocus={field} className={styles.dialog} onRequestClose={close}>
      <form className={styles.dialogContent} onSubmit={submit} noValidate>
        <h2 id={titleId} className={styles.dialogTitle}>
          {title}
        </h2>
        <label htmlFor={fieldId} className={styles.fieldLabel}>
          内容
        </label>
        <textarea
          ref={field}
          id={fieldId}
          className={styles.textarea}
          value={value}
          rows={4}
          aria-describedby={counterId}
          aria-invalid={tooLong ? true : undefined}
          onChange={(event) => setValue(event.target.value)}
        />
        <p id={counterId} className={tooLong ? styles.fieldError : styles.muted}>
          {length} / {maxLength} 字{tooLong ? "，超出了上限" : ""}
        </p>
        {error === null ? null : <FailureNotice error={error} context="write" title="没有保存" messages={messages} />}
        <div className={styles.dialogActions}>
          <Button onClick={close} disabled={pending}>
            取消
          </Button>
          <Button type="submit" variant="primary" busy={pending} disabled={length === 0 || tooLong || unchanged}>
            保存
          </Button>
        </div>
      </form>
    </ModalDialog>
  );
}
