import { Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { isHttpFailure } from "../../../shared/api";
import { Button, TextField } from "../../../shared/ui";
import type { RootCommands } from "../hooks/use-root-commands";
import styles from "./add-root-form.module.css";
import { rootFailureMessages } from "./root-messages";

type AddRootFormProps = {
  commands: RootCommands;
  /** 输入框的内容由区段持有：提交与“重试”成功后都要清空，两者共用同一份状态。 */
  path: string;
  onPathChange: (path: string) => void;
};

/**
 * 新增目录：输入服务器上的绝对路径。结果的提示（已添加、已登记但停用、失败与重试）由 RootCommandNotice 显示，
 * 这里只处理字段本身的错误：没填，或后端以 invalid_library_path 拒绝了这个路径。失败时保留输入，成功后清空。
 */
export function AddRootForm({ commands, path, onPathChange }: AddRootFormProps) {
  const [missing, setMissing] = useState(false);
  const { state } = commands;
  const trimmed = path.trim();
  const busy = state.status === "pending";

  // 只对被拒绝的那个路径显示：用户改了输入，这条错误自然消失，不需要额外的清除逻辑。
  const rejected =
    state.status === "failed" &&
    state.command.kind === "create" &&
    state.command.path === trimmed &&
    isHttpFailure(state.failure, 400, "invalid_library_path");
  const error = missing ? "请输入目录的绝对路径。" : rejected ? rootFailureMessages.invalid_library_path : null;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    if (trimmed === "") {
      setMissing(true);
      return;
    }
    commands.submit({ kind: "create", path: trimmed }, { onSucceeded: () => onPathChange("") });
  }

  return (
    <form className={styles.form} onSubmit={submit} noValidate>
      <TextField
        label="目录路径"
        description="服务器上的绝对路径，例如 /music/classical"
        value={path}
        error={error}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        onChange={(event) => {
          onPathChange(event.target.value);
          setMissing(false);
        }}
      />
      <div>
        <Button type="submit" variant="primary" icon={<Plus size={16} />} busy={busy}>
          {busy && state.command.kind === "create" ? "正在添加…" : "添加目录"}
        </Button>
      </div>
    </form>
  );
}
