import { CircleAlert, X } from "lucide-react";
import { useId, type ClipboardEvent, type KeyboardEvent } from "react";
import { Button } from "../../../shared/ui";
import { addGenres, hasGenreSeparator } from "../field-draft";
import styles from "./genre-tags-input.module.css";

type GenreTagsInputProps = {
  label: string;
  /** 已经加成标签的流派。 */
  list: readonly string[];
  /** 输入框里还没加成标签的内容。 */
  text: string;
  error: string | null;
  onChange: (list: string[], text: string) => void;
};

/**
 * 流派的标签输入：已填的流派是一排小标签，各带删除按钮；回车或输入分号、顿号把输入框里的内容加成标签，
 * 粘贴“a; b”拆成两个；输入框为空时退格删掉最后一个；重复的不再添加。
 */
export function GenreTagsInput({ label, list, text, error, onChange }: GenreTagsInputProps) {
  const id = useId();
  const errorId = `${id}-error`;
  const commit = (value: string) => onChange(addGenres(list, value), "");
  const typeText = (value: string) => {
    if (!hasGenreSeparator(value)) return onChange([...list], value);
    // 分隔符之前的加成标签，之后的留在输入框里继续输入。
    const parts = value.split(/[;；、]/);
    const rest = parts.pop() ?? "";
    onChange(addGenres(list, parts.join(";")), rest);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      commit(text);
    } else if (event.key === "Backspace" && text === "" && list.length > 0) {
      event.preventDefault();
      onChange(list.slice(0, -1), "");
    }
  };
  const onPaste = (event: ClipboardEvent<HTMLInputElement>) => {
    const pasted = event.clipboardData.getData("text");
    if (!hasGenreSeparator(pasted)) return;
    event.preventDefault();
    commit(`${text};${pasted}`);
  };
  return (
    <div className={styles.field}>
      <label htmlFor={id} className={styles.label}>
        {label}
      </label>
      <div className={styles.box} data-invalid={error ? "" : undefined}>
        {list.length === 0 ? null : (
          <ul className={styles.tags}>
            {list.map((genre, index) => (
              <li key={genre} className={styles.tag}>
                <span>{genre}</span>
                <Button variant="icon" icon={<X size={12} />} label={`删除 ${genre}`} onClick={() => onChange(list.filter((_, at) => at !== index), text)} />
              </li>
            ))}
          </ul>
        )}
        <input
          id={id}
          className={styles.input}
          value={text}
          placeholder="添加流派"
          autoComplete="off"
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : undefined}
          onChange={(event) => typeText(event.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
        />
      </div>
      {error ? (
        <p id={errorId} className={styles.error}>
          <CircleAlert size={14} aria-hidden="true" />
          <span>{error}</span>
        </p>
      ) : null}
    </div>
  );
}
