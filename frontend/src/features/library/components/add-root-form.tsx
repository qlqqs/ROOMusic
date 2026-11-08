import { Plus } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { isHttpFailure } from "../../../shared/api";
import { Button, TextField } from "../../../shared/ui";
import type { RootCommands } from "../hooks/use-root-commands";
import styles from "./add-root-form.module.css";
import { FolderBrowser } from "./folder-browser";
import { rootFailureMessages } from "./root-messages";

type AddRootFormProps = {
  commands: RootCommands;
  /** 输入框的内容由区段持有：提交与“重试”成功后都要清空，两者共用同一份状态。 */
  path: string;
  onPathChange: (path: string) => void;
};

/**
 * 新增目录：输入服务器上的绝对路径，或点路径框打开文件夹浏览器选一个（每次打开都从 `/` 开始）。
 * 路径框里按下箭头也能打开；只是 Tab 经过不打开。点浏览器外面或按 Esc 收起，Esc 时焦点回到路径框；在路径框里打字也收起。结果的提示（已添加、已登记但停用、失败与重试）由 RootCommandNotice 显示，
 * 这里只处理字段本身的错误：没填，或后端以 invalid_library_path 拒绝了这个路径。失败时保留输入，成功后清空。
 */
export function AddRootForm({ commands, path, onPathChange }: AddRootFormProps) {
  const [missing, setMissing] = useState(false);
  const [browsing, setBrowsing] = useState(false);
  const browserId = useId();
  const areaRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!browsing) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !areaRef.current?.contains(event.target)) setBrowsing(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [browsing]);

  const closeBrowser = () => {
    setBrowsing(false);
    inputRef.current?.focus();
  };
  const onAreaKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape" && browsing) {
      event.preventDefault();
      closeBrowser();
    }
  };
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
      <div ref={areaRef} className={styles.area} onKeyDown={onAreaKeyDown}>
        <TextField
          ref={inputRef}
          label="目录路径"
          value={path}
          error={error}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          // 可以输入的组合框，弹出的是非模态的文件夹浏览器（role="dialog"）：aria-expanded 只对组合框有效，普通文本框不能用。
          role="combobox"
          aria-haspopup="dialog"
          aria-expanded={browsing}
          aria-controls={browsing ? browserId : undefined}
          onClick={() => setBrowsing(true)}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown" && !browsing) {
              event.preventDefault();
              setBrowsing(true);
            }
          }}
          onChange={(event) => {
            onPathChange(event.target.value);
            setMissing(false);
            setBrowsing(false);
          }}
        />
        {browsing ? (
          <FolderBrowser
            id={browserId}
            onSelect={(selected) => {
              onPathChange(selected);
              setMissing(false);
              closeBrowser();
            }}
          />
        ) : null}
      </div>
      <div>
        <Button type="submit" variant="primary" icon={<Plus size={16} />} busy={busy}>
          {busy && state.command.kind === "create" ? "正在添加…" : "添加目录"}
        </Button>
      </div>
    </form>
  );
}
