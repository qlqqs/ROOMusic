import { CircleAlert, Search, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Button } from "./button";
import styles from "./filter-field.module.css";

/** 写入地址的方式：一次输入过程的第一次写入新增一条历史，之后替换它。 */
export type FilterCommitMode = "push" | "replace";

type FilterFieldProps = {
  /** 输入框的可访问名称（例如“筛选专辑”）。 */
  label: string;
  /** 看得见的占位文字（例如“在专辑中筛选”）；不给时用 label。 */
  placeholder?: string;
  /** 地址里现在的筛选词；没有是 undefined。 */
  value: string | undefined;
  /** 把输入整理成要写进地址的值（去掉首尾空白，空的是 undefined）。 */
  normalize: (text: string) => string | undefined;
  /** 检查输入（例如超长）：不合格时返回要显示的说明，这时不写入地址；合格返回 null。 */
  validate?: (text: string) => string | null;
  /** 写入地址：mode 是新增还是替换历史。 */
  onCommit: (value: string | undefined, mode: FilterCommitMode) => void;
};

/** 停止输入多久之后写入地址。 */
const COMMIT_DELAY_MS = 250;

/**
 * 列表页的即时筛选（专辑、艺人共用）：边输入边出结果，没有提交按钮；框内的 × 清除，Esc 也清除，回车立即写入。
 *
 * 历史记录：一次输入过程只占一条历史——这次过程的第一次写入新增一条（push），之后的写入替换它（replace），
 * 后退回到输入前的列表，不会一个字一个字地退。输入框失去焦点、筛选词被清空（打字删光或点 ×）之后，下一次输入是新的过程。
 * 地址变化（后退、前进、别处改了筛选词）时输入框跟着地址走，还没写入的输入随之放弃。
 * 不合格的输入（validate 给出说明）不写入、也不静默丢弃，说明显示在框下方并与输入框关联。
 */
export function FilterField({ label, placeholder, value, normalize, validate, onCommit }: FilterFieldProps) {
  const id = useId();
  const errorId = `${id}-error`;
  const inputRef = useRef<HTMLInputElement>(null);
  const timer = useRef<number | undefined>(undefined);
  const [draft, setDraft] = useState(value ?? "");
  const [error, setError] = useState<string | null>(null);
  // 上一次渲染时地址里的值，用来发现地址的变化（“按属性调整状态”的写法，不需要 effect）。
  const [seen, setSeen] = useState(value);
  // 自己刚写入、地址还没跟上的值：地址变成它时不重置输入框（框里可能还带着尾随空格，或者已经又多打了几个字）。
  const [awaiting, setAwaiting] = useState<{ value: string | undefined } | null>(null);
  // 这次输入过程是否已经新增过一条历史。
  const [inSession, setInSession] = useState(false);

  if (seen !== value) {
    setSeen(value);
    if (awaiting !== null && awaiting.value === value) setAwaiting(null);
    else {
      // 后退、前进或别处改了筛选词：输入框回到地址里的值，下一次输入是新的过程。
      setAwaiting(null);
      setDraft(value ?? "");
      setError(null);
      setInSession(false);
    }
  }

  useEffect(() => () => window.clearTimeout(timer.current), []);

  function cancelPending() {
    window.clearTimeout(timer.current);
    timer.current = undefined;
  }

  function commit(text: string) {
    cancelPending();
    const problem = validate?.(text) ?? null;
    setError(problem);
    if (problem !== null) return;
    const next = normalize(text);
    if (next === value) return;
    setAwaiting({ value: next });
    onCommit(next, inSession ? "replace" : "push");
    // 筛选词清空了（打字删光）：这次过程结束，下一次输入重新新增一条历史。
    setInSession(next !== undefined);
  }

  function change(text: string) {
    setDraft(text);
    cancelPending();
    timer.current = window.setTimeout(() => {
      timer.current = undefined;
      // 等待期间地址被别处改了，输入框已经回到地址里的值：这次输入作废。
      if (inputRef.current?.value !== text) return;
      commit(text);
    }, COMMIT_DELAY_MS);
  }

  function clear() {
    cancelPending();
    setDraft("");
    setError(null);
    inputRef.current?.focus();
    // 地址里有筛选词，或者自己刚写入、地址还没跟上：都要清掉。
    if (value !== undefined || (awaiting !== null && awaiting.value !== undefined)) {
      setAwaiting({ value: undefined });
      onCommit(undefined, inSession ? "replace" : "push");
    }
    setInSession(false);
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      commit(draft);
    } else if (event.key === "Escape" && draft !== "") {
      event.preventDefault();
      clear();
    }
  }

  function onBlur() {
    // 离开输入框：还在等待的输入立即写入；之后的输入是新的过程。
    if (timer.current !== undefined) commit(draft);
    setInSession(false);
  }

  return (
    <div className={styles.field}>
      <label htmlFor={id} className={styles.label}>
        {label}
      </label>
      <div className={styles.box} data-invalid={error === null ? undefined : ""}>
        <Search size={16} aria-hidden="true" className={styles.icon} />
        <input
          ref={inputRef}
          id={id}
          className={styles.input}
          type="text"
          inputMode="search"
          enterKeyHint="search"
          autoComplete="off"
          spellCheck={false}
          placeholder={placeholder ?? label}
          value={draft}
          aria-invalid={error === null ? undefined : true}
          aria-describedby={error === null ? undefined : errorId}
          onChange={(event) => change(event.target.value)}
          onKeyDown={onKeyDown}
          onBlur={onBlur}
        />
        {draft === "" ? null : (
          <span className={styles.clear}>
            {/* 按下 × 不让输入框失去焦点：否则失焦会先把还在等待的输入写进地址，再被清除，多出一条历史。 */}
            <Button variant="icon" icon={<X size={14} />} label="清除筛选" onMouseDown={(event) => event.preventDefault()} onClick={clear} />
          </span>
        )}
      </div>
      {error === null ? null : (
        <p id={errorId} className={styles.error}>
          <CircleAlert size={14} aria-hidden="true" />
          <span>{error}</span>
        </p>
      )}
    </div>
  );
}
