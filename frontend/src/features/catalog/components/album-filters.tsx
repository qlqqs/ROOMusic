import { useNavigate } from "@tanstack/react-router";
import { useRef, useState, type FormEvent } from "react";
import { Button, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { MAX_SEARCH_BYTES, parseSearchText, type AlbumSearch } from "../album-search";
import styles from "./album-filters.module.css";

/**
 * 搜索与筛选：搜索词显式提交（按钮或回车），每次提交一条历史；“只看需要关注”立即生效；提交与切换筛选都回到第 1 页。
 * 输入框的内容在地址变化（后退、前进、清除）时跟着地址走，没有提交的草稿不写进地址。
 */
export function AlbumFilters({ search }: { search: AlbumSearch }) {
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(search.q ?? "");
  const [syncedQuery, setSyncedQuery] = useState(search.q);
  const [error, setError] = useState<string | null>(null);
  // 地址里的搜索词变了（后退、前进、清除）：输入框回到地址里的值。这是 React 推荐的“按属性重置状态”写法，不需要 effect。
  if (syncedQuery !== search.q) {
    setSyncedQuery(search.q);
    setDraft(search.q ?? "");
    setError(null);
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const bytes = utf8ByteLength(draft.trim());
    if (bytes > MAX_SEARCH_BYTES) {
      // 不静默丢弃：提示搜索词太长。
      setError("搜索词太长了，请短一些。");
      return;
    }
    setError(null);
    void navigate({ to: "/albums", search: { q: parseSearchText(draft), attention: search.attention, page: undefined } });
  };

  const clear = () => {
    setDraft("");
    setError(null);
    inputRef.current?.focus();
    void navigate({ to: "/albums", search: { q: undefined, attention: search.attention, page: undefined } });
  };

  return (
    <form role="search" aria-label="搜索专辑" className={styles.form} onSubmit={submit}>
      <div className={styles.input}>
        <TextField
          ref={inputRef}
          label="搜索专辑"
          description="按标题、艺人或曲目名查找。"
          name="q"
          type="text"
          inputMode="search"
          enterKeyHint="search"
          autoComplete="off"
          value={draft}
          error={error}
          onChange={(event) => setDraft(event.target.value)}
        />
      </div>
      <div className={styles.actions}>
        <Button type="submit" variant="primary">
          搜索
        </Button>
        {search.q === undefined ? null : <Button onClick={clear}>清除</Button>}
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={search.attention === "required"}
            onChange={(event) => void navigate({ to: "/albums", search: { q: search.q, attention: event.target.checked ? "required" : undefined, page: undefined } })}
          />
          <span>只看需要关注</span>
        </label>
      </div>
    </form>
  );
}
